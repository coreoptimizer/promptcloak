# Roadmap

The MVP scope is **inbound prompt tokenization + response re-hydration** for
chat-style LLM traffic. This document tracks the agreed-upon next increments and
other hardening work. Items are roughly ordered by priority.

## 1. Outbound PII detection (deferred from MVP)

Today the response path only *re-hydrates this gateway's own tokens*. It does
**not** detect PII that the model itself emits (e.g. data it inferred, retrieved,
or hallucinated, or PII that entered via a path we didn't tokenize).

- Run the same Presidio detection on streamed response chunks.
- Challenge: detection over a *stream* requires sentence/word-boundary buffering
  so a PII span isn't missed when split across chunks, more involved than the
  re-hydration carry buffer (which keys off a known sentinel).
- Decide per-entity action on outbound (mask, block, annotate); likely reuse the
  policy engine from item 4.

## 2. MCP tool-call inspection (deferred from MVP)

Inspect [Model Context Protocol](https://modelcontextprotocol.io/) tool-call
requests and tool results flowing through the gateway.

- Parse JSON-RPC `tools/call` params and tool result payloads.
- Tokenize sensitive arguments before they reach a tool; re-hydrate results.
- Distinct content shape from chat completions; extend `internal/llmbody` (or a
  sibling `mcpbody` package) with MCP framing.

## 3. RAG / context payload inspection (deferred from MVP)

Inspect retrieved documents / injected context before they reach the model.

- Often the same chat body (system / context messages already walked), but may
  also arrive via provider-specific `attachments` / `file` / `context` fields.
- Consider allow/deny by source and size limits.

## 4. Policy engine (per-category actions)

Replace the single global "tokenize everything" behavior with configurable
per-entity-type policy, ideally a CRD:

- Actions: `tokenize` (reversible), `mask` (irreversible), `block`, `audit-only`.
- Example: `block` on secrets, `tokenize` PII, `audit` low-risk identifiers.
- Per-route / per-namespace policy attachment.

## 5. Secrets & credentials detection

Presidio is PII-focused. Add credential/secret detection (API keys, tokens,
private keys) via regex rulesets (à la gitleaks/detect-secrets) as Presidio
ad-hoc recognizers or an in-process pre-pass, typically a `block` action.

## 6. Detokenization & audit API

- Authenticated endpoint to resolve a token → value for authorized auditing.
- Structured audit log of every detection (entity type, action, route, token id,
  never the raw value) for compliance evidence.

## 7. Per-session token salting

Derive the token-id salt per session/tenant (from a header or mTLS identity) so
identical PII does not yield identical token ids across sessions, preventing
cross-request correlation. (Today: static `TOKEN_SALT`.)

## 8. Observability

- Prometheus metrics: detections by entity type, tokenize/rehydrate latency,
  Presidio error rate, vault hit/miss, fail-open events.
- Tracing spans on the ext_proc stream.
- Richer `/readyz` that checks Presidio + Valkey reachability.

## 9. Robustness & scale

- Large request bodies: support `Streamed` request mode with boundary-aware
  detection instead of `Buffered`.
- Broaden provider body coverage (Gemini, Bedrock, Cohere shapes).
- Fail-closed as the default once detection availability is proven.
- Valkey HA / persistence guidance; vault encryption at rest.
