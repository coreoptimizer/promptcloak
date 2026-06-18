// Package extproc implements the Envoy external-processing (ext_proc) gRPC
// service. Envoy opens one bidirectional stream per HTTP transaction and sends
// the request, then the response, on that same stream — so a single Process
// call sees both directions and can tokenize the request and re-hydrate the
// response with shared state.
package extproc

import (
	"context"
	"errors"
	"io"
	"log/slog"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"github.com/coreoptimizer/promptcloak/internal/llmbody"
	"github.com/coreoptimizer/promptcloak/internal/redact"
	"github.com/coreoptimizer/promptcloak/internal/rehydrate"
	"github.com/coreoptimizer/promptcloak/internal/vault"
)

// Server is the ext_proc ExternalProcessor implementation.
type Server struct {
	extprocv3.UnimplementedExternalProcessorServer
	redactor *redact.Redactor
	vault    vault.Vault
	failOpen bool
	log      *slog.Logger
}

// NewServer builds the ext_proc server.
func NewServer(r *redact.Redactor, v vault.Vault, failOpen bool, log *slog.Logger) *Server {
	return &Server{redactor: r, vault: v, failOpen: failOpen, log: log}
}

// Process handles a single HTTP transaction's ext_proc stream.
func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	ctx := stream.Context()
	rehy := rehydrate.New(s.vault)

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		var resp *extprocv3.ProcessingResponse
		switch v := req.Request.(type) {
		case *extprocv3.ProcessingRequest_RequestHeaders:
			resp = requestHeadersResponse()
		case *extprocv3.ProcessingRequest_RequestBody:
			resp = s.onRequestBody(ctx, v.RequestBody)
		case *extprocv3.ProcessingRequest_ResponseHeaders:
			resp = continueHeaders(true)
		case *extprocv3.ProcessingRequest_ResponseBody:
			resp = s.onResponseBody(ctx, rehy, v.ResponseBody)
		case *extprocv3.ProcessingRequest_RequestTrailers:
			resp = continueTrailers(false)
		case *extprocv3.ProcessingRequest_ResponseTrailers:
			resp = continueTrailers(true)
		default:
			resp = continueHeaders(false)
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// onRequestBody sanitizes the (buffered) request body before it reaches the
// model. The request body mode is configured as Buffered, so this receives the
// whole body in one message.
func (s *Server) onRequestBody(ctx context.Context, body *extprocv3.HttpBody) *extprocv3.ProcessingResponse {
	sanitized, err := llmbody.SanitizeRequest(ctx, body.GetBody(), s.redactor)
	if err != nil {
		s.log.Warn("request inspection failed", "error", err, "fail_open", s.failOpen)
		if !s.failOpen {
			return immediateError(typev3.StatusCode_ServiceUnavailable,
				"promptcloak: request inspection failed")
		}
		sanitized = body.GetBody()
	}
	return bodyMutation(false, sanitized)
}

// onResponseBody re-hydrates streamed response chunks. The response body mode is
// configured as Streamed, so this is called once per chunk. Re-hydration always
// fails open: a failed lookup must not break the user's response stream.
func (s *Server) onResponseBody(ctx context.Context, rehy *rehydrate.Rehydrator, body *extprocv3.HttpBody) *extprocv3.ProcessingResponse {
	out, err := rehy.Process(ctx, body.GetBody(), body.GetEndOfStream())
	if err != nil {
		s.log.Warn("response re-hydration failed", "error", err)
		out = body.GetBody()
	}
	return bodyMutation(true, out)
}

// requestHeadersResponse continues request processing but strips the
// Content-Length header. Tokenization changes the body length, and Envoy does
// not recompute Content-Length on a buffered body mutation — a stale value
// yields "mismatch_between_content_length_and_the_length_of_the_mutated_body"
// (HTTP 500). Removing it lets Envoy recompute the length from the buffered
// (mutated) body before forwarding upstream.
func requestHeadersResponse() *extprocv3.ProcessingResponse {
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					Status: extprocv3.CommonResponse_CONTINUE,
					HeaderMutation: &extprocv3.HeaderMutation{
						RemoveHeaders: []string{"content-length"},
					},
				},
			},
		},
	}
}

func continueHeaders(response bool) *extprocv3.ProcessingResponse {
	hr := &extprocv3.HeadersResponse{
		Response: &extprocv3.CommonResponse{Status: extprocv3.CommonResponse_CONTINUE},
	}
	if response {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{ResponseHeaders: hr},
		}
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{RequestHeaders: hr},
	}
}

func continueTrailers(response bool) *extprocv3.ProcessingResponse {
	tr := &extprocv3.TrailersResponse{}
	if response {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseTrailers{ResponseTrailers: tr},
		}
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestTrailers{RequestTrailers: tr},
	}
}

// bodyMutation builds a CONTINUE response that replaces the body chunk with
// newBody. In buffered request mode Envoy recomputes Content-Length; in streamed
// response mode the replacement chunk may differ in size from the original.
func bodyMutation(response bool, newBody []byte) *extprocv3.ProcessingResponse {
	br := &extprocv3.BodyResponse{
		Response: &extprocv3.CommonResponse{
			Status: extprocv3.CommonResponse_CONTINUE,
			BodyMutation: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{Body: newBody},
			},
		},
	}
	if response {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{ResponseBody: br},
		}
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{RequestBody: br},
	}
}

func immediateError(code typev3.StatusCode, msg string) *extprocv3.ProcessingResponse {
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status: &typev3.HttpStatus{Code: code},
				Body:   []byte(msg),
			},
		},
	}
}
