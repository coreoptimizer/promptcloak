# promptcloak

A compliance gateway extension for Kubernetes that enforces data-protection
policy on LLM/chat traffic. It sits in the request path as an **Envoy
`ext_proc` service** behind a [Gateway API](https://gateway-api.sigs.k8s.io/)
data plane and **reversibly tokenizes PII** before prompts reach a chatbot,
then **re-hydrates** the real values on the way back to the user.

The model never sees the raw sensitive data; the user never sees a redaction.

```
 user / agent
     │  POST /v1/chat/completions  {"messages":[{"role":"user","content":"I'm Jane Doe, jane@acme.com"}]}
     ▼
┌─────────────────┐   ext_proc gRPC    ┌────────────────────────┐     ┌──────────────────┐
│  Envoy Gateway  │ ─────────────────► │  promptcloak-extproc    │ ──► │ Presidio analyzer│
│  (Gateway API)  │ ◄───────────────── │  (this project, Go)    │ ◄── │  (PII detection) │
└────────┬────────┘                    └───────────┬────────────┘     └──────────────────┘
         │                                          │  token ⇄ value
         │  sanitized body                          ▼
         │  {"...content":"I'm [[CMPL_PERSON_…]], [[CMPL_EMAIL_ADDRESS_…]]"}   ┌────────┐
         ▼                                                                     │ Redis  │
   LLM provider  ──── response (may echo tokens) ────► re-hydrate ──► user     │ vault  │
                                                                               └────────┘
```

## How it works

1. **Attach.** An `EnvoyExtensionPolicy` wires this gRPC service into an
   `HTTPRoute`. Envoy streams each transaction to it.
2. **Inbound (request).** The request body is delivered buffered. The service
   parses the chat JSON (OpenAI / Anthropic shapes), sends user-authored text to
   **Presidio** for PII detection, replaces each detected span with a
   deterministic **token** (`[[CMPL_<ENTITY>_<id>]]`), stores `token → value`
   in **Redis**, and forwards the sanitized body to the model.
3. **Outbound (response).** Response chunks are delivered streamed. The service
   swaps any of *its* tokens back to the real values (a cheap, streaming-aware
   lookup) so the end user sees their real data restored.

This is the MVP scope: **inbound prompt tokenization + response re-hydration**.
Outbound PII *detection*, MCP tool-call inspection, and RAG/context inspection
are tracked in [ROADMAP.md](./ROADMAP.md). Design details are in
[ARCHITECTURE.md](./ARCHITECTURE.md).

## Repository layout

```
cmd/extproc            ext_proc gRPC server entrypoint
internal/config        environment-driven configuration
internal/detect        Presidio analyzer client (PII detection)
internal/tokenize      reversible token format
internal/vault         token store (Redis + in-memory)
internal/redact        detect + tokenize + persist  (the request-side transform)
internal/llmbody       chat-body JSON walker (OpenAI / Anthropic shapes)
internal/rehydrate     streaming-aware token → value restoration (response side)
internal/extproc       Envoy ExternalProcessor implementation
deploy/k8s             Gateway API + ext_proc + Redis + Presidio manifests
```

## Quickstart (local cluster)

Prerequisites: a cluster (e.g. `kind`), `kubectl`, and **Envoy Gateway**:

```sh
# Validated against Envoy Gateway v1.8.1 (any recent v1.x with the
# EnvoyExtensionPolicy CRD should work).
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  -n envoy-gateway-system --create-namespace
kubectl wait --for=condition=Available -n envoy-gateway-system deploy/envoy-gateway --timeout=180s
```

> **Note on detection tuning.** Detection quality is governed entirely by
> Presidio config, not this gateway. Out of the box the analyzer image catches
> PERSON / EMAIL_ADDRESS / LOCATION well, but the phone recognizer is
> threshold-sensitive (lower `PRESIDIO_SCORE_THRESHOLD` to ~0.3 to catch more)
> and the default image has **no effective US_SSN recognizer** — add a custom
> recognizer for SSNs/secrets (see [ROADMAP.md](./ROADMAP.md) items 4–5).

Build and load the image, then deploy:

```sh
make docker-build IMG=ghcr.io/coreoptimizer/promptcloak/extproc:dev
kind load docker-image ghcr.io/coreoptimizer/promptcloak/extproc:dev
# point the Deployment at your tag, then:
make deploy
```

Send a request through the gateway (mock upstream echoes what it received):

```sh
kubectl -n promptcloak-system port-forward svc/promptcloak-gateway 8080:80 &
curl -s localhost:8080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"I am Jane Doe, email jane@acme.com"}]}' | jq
```

- The echo server's logs show the body it **received** — names/emails replaced by
  `[[CMPL_…]]` tokens (the "model" never saw PII).
- The response you get back has those tokens **re-hydrated** to the real values.

## Configuration

All via environment (see [`30-extproc.yaml`](./deploy/k8s/30-extproc.yaml)):

| Variable | Default | Purpose |
|---|---|---|
| `LISTEN_ADDR` | `:9002` | gRPC ext_proc listen address |
| `HEALTH_ADDR` | `:8080` | HTTP `/healthz` + `/readyz` |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `FAIL_OPEN` | `true` | forward (vs. reject) when a body can't be inspected |
| `PRESIDIO_URL` | `http://presidio-analyzer.promptcloak-system.svc:3000` | analyzer endpoint |
| `PRESIDIO_LANGUAGE` | `en` | detection language |
| `PRESIDIO_SCORE_THRESHOLD` | `0.5` | minimum confidence to act on |
| `PRESIDIO_ENTITIES` | *(all)* | comma-separated entity allowlist |
| `REDIS_ADDR` | *(unset → in-memory)* | vault backend |
| `REDIS_PASSWORD` / `REDIS_DB` | `` / `0` | vault auth / db |
| `TOKEN_TTL` | `24h` | mapping lifetime |
| `TOKEN_SALT` | `promptcloak` | secret mixed into token ids — **change this** |

## Security notes

- **Fail-open vs fail-closed.** The MVP defaults to `FAIL_OPEN=true` so a
  Presidio outage doesn't break all chat traffic. In regulated environments set
  `FAIL_OPEN=false` to reject un-inspectable requests with `503`.
- **`TOKEN_SALT`** is a secret. With a weak/shared salt, identical PII produces
  identical token ids across requests, enabling correlation. Per-session salting
  is on the roadmap.
- **Vault durability.** Use Redis (default in the manifests) so tokens survive
  across replicas and restarts. The in-memory vault is dev-only.

## Development

```sh
make build   # compile ./bin/extproc
make test    # unit tests
make vet
```
