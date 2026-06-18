// Package llmbody walks chat-style LLM request bodies and applies a text
// Transformer to every user-authored text field it recognizes.
//
// It understands the common shapes without committing to a single provider's
// full schema:
//
//   - OpenAI Chat Completions: messages[].content as a string, or as an array
//     of content parts each with a "text" field.
//   - OpenAI Responses API: top-level "input" (string or parts).
//   - Anthropic Messages: top-level "system" (string or text blocks) and
//     messages[].content (string or text blocks).
//   - Legacy completions: top-level "prompt".
//
// Fields that are not recognized (model, role, tool schemas, image URLs, …) are
// left untouched. Bodies that are not JSON objects pass through unchanged.
package llmbody

import (
	"context"
	"encoding/json"
)

// Transformer transforms a single piece of text (e.g. detects + tokenizes PII).
type Transformer interface {
	Transform(ctx context.Context, text string) (string, error)
}

// SanitizeRequest applies tr to every recognized text field in body and returns
// the re-serialized JSON. If body is not a JSON object it is returned unchanged.
// If nothing was changed the original bytes are returned verbatim (preserving
// formatting and byte-for-byte fidelity).
func SanitizeRequest(ctx context.Context, body []byte, tr Transformer) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		// Not a JSON object we understand — leave it alone.
		return body, nil
	}

	changed := false

	// Top-level single-content fields shared across providers/APIs.
	for _, key := range []string{"system", "prompt", "input"} {
		if v, ok := root[key]; ok {
			nv, ch, err := transformContent(ctx, tr, v)
			if err != nil {
				return nil, err
			}
			if ch {
				root[key] = nv
				changed = true
			}
		}
	}

	// Chat messages: messages[].content
	if msgs, ok := root["messages"].([]any); ok {
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			nv, ch, err := transformContent(ctx, tr, mm["content"])
			if err != nil {
				return nil, err
			}
			if ch {
				mm["content"] = nv
				changed = true
			}
		}
	}

	if !changed {
		return body, nil
	}
	return json.Marshal(root)
}

// transformContent handles the two content shapes: a plain string, or an array
// of content parts/blocks each carrying a "text" field. It returns the
// (possibly mutated) value and whether anything changed.
func transformContent(ctx context.Context, tr Transformer, v any) (any, bool, error) {
	switch c := v.(type) {
	case string:
		nv, err := tr.Transform(ctx, c)
		if err != nil {
			return v, false, err
		}
		return nv, nv != c, nil

	case []any:
		changed := false
		for _, part := range c {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			t, ok := pm["text"].(string)
			if !ok {
				continue
			}
			nt, err := tr.Transform(ctx, t)
			if err != nil {
				return v, false, err
			}
			if nt != t {
				pm["text"] = nt
				changed = true
			}
		}
		return c, changed, nil
	}

	return v, false, nil
}
