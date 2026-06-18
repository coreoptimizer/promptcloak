# Proposal: pluggable frontends (use promptcloak beyond Envoy Gateway)

- Status: Draft
- Date: 2026-06-18
- Scope: packaging + thin adapters; keeps all current features working

## Summary

promptcloak's PII tokenization core is already transport-neutral. The only code
tied to Envoy is the ext_proc adapter and its wiring. This proposal promotes the
core to a small public **Engine** API and adds thin **frontends** over it, led by
a **standalone reverse proxy** that needs no gateway or Kubernetes. The Envoy
ext_proc path is preserved unchanged as one frontend among several.

## Goals

- Run the same tokenize/re-hydrate logic outside Envoy Gateway: as a standalone
  HTTP reverse proxy, and as an embeddable Go library.
- Keep every current feature and behavior intact (ext_proc, detection backends,
  Valkey vault, streaming re-hydration, Helm/release).
- Keep the public API surface small and stable.

## Non-goals

- Rewriting the detection, tokenization, vault, or body-walking logic.
- Supporting streaming _request_ bodies (the transform needs the whole body, as
  ext_proc's `Buffered` mode already requires).
- Outbound PII detection, MCP, or RAG inspection (tracked separately in
  ROADMAP.md).

## Background: the core is already decoupled

Envoy/gRPC are imported by exactly two files: `internal/extproc/server.go` and
`cmd/extproc/main.go`. Everything else operates on plain bytes.

| Package                                 | Role                                                                                            | Transport-coupled?       |
| --------------------------------------- | ----------------------------------------------------------------------------------------------- | ------------------------ |
| `tokenize`, `detect`, `vault`, `redact` | token format, pluggable detection, vault, request transform                                     | No (pure)                |
| `llmbody`                               | `SanitizeRequest(ctx, body []byte, Transformer) ([]byte, error)`; walks OpenAI/Anthropic shapes | No (bytes in, bytes out) |
| `rehydrate`                             | `Rehydrator.Process(ctx, chunk []byte, eos bool) ([]byte, error)`; streaming-aware              | No (bytes in, bytes out) |
| `internal/extproc`                      | translates Envoy ext_proc messages into the two calls above                                     | Yes (only this)          |
| `cmd/extproc`                           | gRPC server + config + vault + detector wiring                                                  | Yes                      |

The ext_proc adapter is ~180 lines that do nothing but map Envoy messages onto
a request-sanitize call and a streaming response-re-hydrate call. That mapping is
the seam every other frontend reuses.

## Proposed design

### 1. A public Engine facade

The core currently lives under `internal/`, so no other module can import it.
Add one small exported package (proposed: `pkg/promptcloak`) that bundles
construction plus the two operations, and keep everything else `internal/` so the
public surface stays minimal:

```go
package promptcloak

type Engine struct { /* configured redactor: detect + vault */ }

func New(opts Options) (*Engine, error) // programmatic construction
func FromEnv() (*Engine, error)         // wraps internal/config (existing env knobs)

// Request side: sanitize a full chat body. On inspection error, returns the
// original bytes plus the error so the caller applies its own fail-open policy.
func (e *Engine) SanitizeRequest(ctx context.Context, body []byte) ([]byte, error)

// Response side: one Rehydrator per response stream.
func (e *Engine) NewRehydrator() *Rehydrator
// Rehydrator.Process(ctx, chunk []byte, eos bool) ([]byte, error)
```

This is almost pure re-export: `SanitizeRequest` is `llmbody.SanitizeRequest`
wired to a configured `redact.Redactor`; `Rehydrator` re-exports
`rehydrate.Rehydrator`. No behavior change.

### 2. Frontends as thin adapters over Engine

- **Standalone reverse proxy (priority).** A small HTTP server: read request body
  -> `SanitizeRequest` -> forward to a configurable upstream -> stream the
  response back through a `Rehydrator`. No Envoy, no Gateway API, no Kubernetes
  required. Deployable as a bare process, a container, or a sidecar in front of
  any LLM endpoint.
- **Go library / SDK.** Any Go service that already proxies LLM traffic imports
  `Engine` directly. Zero infrastructure.
- **Envoy ext_proc (existing).** Refactored to call `Engine` instead of reaching
  into `llmbody`/`rehydrate` directly. Identical behavior; existing tests pass.
- **Future, same seam.** Envoy WASM filter, NGINX/Kong external processors, an
  MCP-aware middleware, or a `promptcloak redact` CLI for offline tokenization.

### 3. Binary layout: one binary, subcommands

Ship a single binary with subcommands rather than multiple binaries:

```
promptcloak extproc   # the gRPC ext_proc server (today's cmd/extproc)
promptcloak proxy     # the standalone reverse proxy (new)
```

One image, one release artifact; the Helm chart selects the mode via container
args. Lower build/release/versioning surface than separate binaries.

### Reverse proxy: behavior detail

Because it is the priority frontend, the contract:

- **Request:** read the full body, run `SanitizeRequest`, recompute
  `Content-Length`, forward to the configured upstream with the original method,
  path, and headers (including `Authorization`) passed through.
- **Response:** stream upstream chunks through a per-request `Rehydrator`, writing
  and flushing each transformed chunk (`http.Flusher`) so SSE / chunked responses
  stay live; flush the carry buffer at end of stream.
- **Passthrough:** non-JSON or unrecognized bodies pass through untouched (the
  core already no-ops on these); fail-open vs fail-closed reuses the existing
  policy on inspection error.
- **Config:** upstream base URL (and optional per-path routing), listen address,
  plus all existing detection/vault knobs via `FromEnv()`.

## What stays vs. changes

- **Unchanged logic:** `tokenize`, `detect`, `vault`, `redact`, `llmbody`,
  `rehydrate`, their tests, ext_proc behavior, Helm chart, release pipeline.
- **New:** `pkg/promptcloak` facade; the reverse-proxy frontend; a proxy e2e test
  against the existing mock upstream; a `proxy` subcommand.
- **Light refactor:** `internal/extproc` calls `Engine`; `cmd/` consolidates into
  one binary with `extproc` and `proxy` subcommands.

## Risks and decisions

- **Public API is a compatibility commitment.** Keep the exported surface to
  `Engine`, `Options`, and `Rehydrator` only; leave detection/vault internals
  unexported to minimize the blast radius of future changes.
- **Buffered request only.** `SanitizeRequest` needs the whole body. The proxy
  reads the full request body and recomputes `Content-Length`; streaming request
  bodies remain out of scope.
- **Faithful proxy passthrough.** Headers (auth), status codes, trailers, and
  streaming semantics must be preserved; non-chat traffic must pass through inert.
- **Config dual-path.** Keep env config working via `FromEnv()`; add programmatic
  `Options` for library users.

## Phased plan

1. **Extract the Engine facade** and point `internal/extproc` at it. Pure
   refactor, no behavior change, existing tests stay green. Unblocks everything.
2. **Standalone reverse proxy:** the `proxy` subcommand built on `Engine`, with
   streaming re-hydration, an e2e test against the mock upstream, a Dockerfile
   target, and a `mode` toggle in the Helm chart.
3. **(Optional/future)** additional adapters (WASM, MCP, CLI) as independently
   shippable frontends on the same seam.

Phases 1 and 2 deliver the goal; phase 3 is opportunistic.

## Open questions

- Exported package path: `pkg/promptcloak` vs a top-level `promptcloak` package.
- Proxy routing: single upstream base URL initially, or path/host-based routing
  to multiple providers from the start?
- Helm: one chart with a `mode` value, or a subchart per frontend?
