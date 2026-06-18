# Architecture

![promptcloak architecture diagram](arch.png)

## Why ext_proc behind Gateway API

The [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) is the official
SIG-Network project for L4/L7 routing (the successor to Ingress). Rather than
build a bespoke proxy, this project plugs into a conformant Envoy-based data
plane ([Envoy Gateway](https://gateway.envoyproxy.io/)) using Envoy's
**external processing (`ext_proc`)** filter.

`ext_proc` is a bidirectional gRPC stream: for each HTTP transaction Envoy sends
the request (and later the response) to an external service, which may inspect
and **mutate** headers, body and trailers, or return an immediate response. This
gives us full request/response body access in a language-agnostic, sidecar-free,
horizontally-scalable way — without forking the proxy.

Key property we rely on: **one gRPC stream carries both directions of a single
HTTP transaction.** So a single `Process` invocation sees the request *and* its
response, and request-side tokenization and response-side re-hydration share
context.

## Request lifecycle

```
Envoy                         promptcloak-extproc
  │  RequestHeaders ─────────────►│  CONTINUE
  │  RequestBody (Buffered) ─────►│  parse chat JSON
  │                               │   └─ Presidio /analyze → spans
  │                               │   └─ tokenize spans, Redis SET token→value
  │  ◄──────── BodyMutation ──────│  sanitized body (Content-Length recomputed)
  │  … forwards to LLM provider …
  │  ResponseHeaders ────────────►│  CONTINUE
  │  ResponseBody (Streamed) ────►│  re-hydrate tokens (chunk by chunk)
  │  ◄──────── BodyMutation ──────│  restored chunk
  │  ResponseBody (eos) ─────────►│  flush carry
  │  ◄──────── BodyMutation ──────│
```

- **Request body = `Buffered`**: the whole body arrives in one message so it can
  be parsed and rewritten atomically. Envoy recomputes `Content-Length` after a
  buffered body mutation.
- **Response body = `Streamed`**: chunks arrive as the model produces them, so
  re-hydration is SSE-friendly and never blocks the token stream.

## Components

### `internal/llmbody` — body walking
Parses the JSON object and applies a text transform to the fields that carry
user-authored text, across provider shapes:
- OpenAI Chat: `messages[].content` (string or `[{type,text}]` parts)
- OpenAI Responses: top-level `input`
- Anthropic Messages: `system` + `messages[].content` (string or text blocks)
- Legacy completions: `prompt`

Everything else (model id, roles, tool schemas, image URLs) is untouched. If the
body isn't a JSON object, or nothing matched, the original bytes are returned
verbatim (no needless re-serialization).

### `internal/detect` — pluggable PII detection
Detection sits behind one interface so the provider is swappable without
touching anything downstream:

```go
type Analyzer interface {
    Analyze(ctx context.Context, text, language string) ([]Finding, error)
}
```

`detect.New(Options)` constructs the backend chosen by `DETECTOR`:

- **`presidio`** (default) — POSTs `{text, language, score_threshold, entities}`
  to the Presidio analyzer's `/analyze` and maps the
  `[{entity_type, start, end, score}]` response into `Finding`s. Only the
  analyzer service is required; tokenization is done in-process.
- **`regex`** — a dependency-free matcher for structurally-regular entities
  (EMAIL_ADDRESS, US_SSN, CREDIT_CARD, PHONE_NUMBER, IP_ADDRESS, URL). No
  external service; ideal for local dev, air-gapped installs, and tests.
- **`gcpdlp`** — Google Cloud DLP's `content:inspect` REST API. DLP returns
  code-point ranges directly (no offset conversion needed) and authenticates
  with a static token or the GCE/GKE metadata server (workload identity).

Two invariants make the backends interchangeable, enforced by each adapter:

> **Offsets are runes.** A `Finding`'s `Start`/`End` are code-point offsets, not
> bytes. The redactor slices on `[]rune`, so accented names and emoji don't
> corrupt surrounding text. Presidio and DLP already report code points; the
> regex backend converts its byte offsets (`runeOffsetIndex`). (`internal/redact`
> test `TestTransformUnicodeOffsets`, `internal/detect` test
> `TestRegexAnalyzeFindsAndOffsets`.)

> **Canonical entity vocabulary.** `EntityType` uses Presidio-style names
> (`PERSON`, `EMAIL_ADDRESS`, …) regardless of backend, so token labels stay
> stable. The DLP adapter normalizes DLP info-type names (e.g.
> `US_SOCIAL_SECURITY_NUMBER` → `US_SSN`); the `DETECT_ENTITIES` allowlist is
> expressed in these canonical names.

### `internal/redact` — the request-side transform
Detects, resolves overlapping spans (keep the longest/highest-scoring,
non-overlapping set), replaces each with a token, and persists `token→value`.

### `internal/tokenize` — token format
```
[[CMPL_<ENTITY>_<id>]]      e.g. [[CMPL_EMAIL_ADDRESS_9f8e7d6c5b4a3210]]
```
- **ASCII + bracket-delimited** → survives JSON encoding, LLM tokenizers, and SSE
  framing, and is locatable on the response path.
- **`id = sha256(salt ⧺ value)[:16]`** → deterministic, so the same value maps to
  the same token (preserving coreference for the model) while never embedding the
  value. Reversal is by vault lookup, not by inverting the hash.

### `internal/vault` — token store
`Put`/`Get` with TTL. **Redis** (shared, durable, multi-replica — production) or
**in-memory** (dev/test). Because each ext_proc replica may handle either
direction of different transactions behind a load balancer, a shared vault makes
re-hydration robust to scaling.

### `internal/rehydrate` — streaming re-hydration
Restoring tokens in a *stream* is the subtle part: a token can be split across
chunk boundaries. The `Rehydrator`:
1. prepends bytes carried over from the previous chunk;
2. replaces every **complete** token via vault lookup (unknown tokens are left
   intact);
3. if not end-of-stream, computes the longest trailing slice that could be the
   start of a token (an unterminated `[[CMPL_…` or a prefix of the marker) and
   **holds it back** as carry — bounded by `tokenize.MaxLen` so a stray `[[`
   can't grow the buffer without limit;
4. on end-of-stream, flushes everything.

The unit test feeds payloads at **every** chunk size (1..N) to prove correctness
at every possible split point.

## Failure modes

- **Request inspection fails** (detection backend down/unauthorized, bad body):
  governed by `FAIL_OPEN`. Open → forward original (availability); closed →
  `503` (safety).
- **Response re-hydration fails**: always fail-open — never break a user's
  in-flight response. Worst case the user sees a raw token instead of the value.
- **Vault miss on response**: the token is emitted verbatim (visible but inert).

## Scaling & ordering notes

- The service is stateless except for the per-stream re-hydration carry buffer;
  scale `replicas` freely with Redis as the shared vault.
- gRPC to the ext_proc backend is HTTP/2; the Service advertises
  `appProtocol: kubernetes.io/h2c` so Envoy Gateway uses h2c upstream.
- Determinism means repeated values in one prompt collapse to one token — fewer
  vault writes and consistent references in the model's view.
