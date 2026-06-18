// Package vault stores reversible token->value mappings.
//
// The inbound path writes a mapping for every PII span it tokenizes; the
// outbound path reads it back to re-hydrate the model's response. Two
// implementations are provided: a Redis-backed vault (shared, durable, TTL'd —
// the production choice) and an in-memory vault (single-replica, non-durable —
// for local dev and tests).
package vault

import "context"

// Vault is the token store contract.
type Vault interface {
	// Put stores token->value with the vault's configured TTL. It is
	// idempotent: storing the same token again simply refreshes it.
	Put(ctx context.Context, token, value string) error
	// Get returns the value for a token. ok is false when the token is unknown
	// or has expired.
	Get(ctx context.Context, token string) (value string, ok bool, err error)
	// Close releases any resources held by the vault.
	Close() error
}
