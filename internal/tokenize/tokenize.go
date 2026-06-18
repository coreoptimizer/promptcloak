// Package tokenize creates and recognizes reversible compliance tokens.
//
// A token is an ASCII sentinel of the form:
//
//	[[CMPL_<ENTITY>_<id>]]
//
// where <ENTITY> is the (normalized) Presidio entity type — e.g. PERSON,
// EMAIL_ADDRESS — and <id> is a hex digest derived from the original value plus
// a static salt.
//
// The format is deliberately:
//   - ASCII and bracket-delimited, so it survives JSON encoding, virtually every
//     LLM tokenizer, and Server-Sent-Events framing intact;
//   - deterministic, so the same value yields the same token within (and across)
//     requests, preserving coreference for the model;
//   - locatable, so the response path can find tokens again and swap the real
//     values back in (re-hydration).
//
// The token never encodes the value itself — only a digest of it. Reversal is
// done by looking the token up in the vault, not by inverting the hash.
package tokenize

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const (
	prefix = "[[CMPL_"
	suffix = "]]"

	// idLen is the number of hex chars of the digest used as the token id.
	// 16 hex chars = 64 bits, making accidental collisions between distinct
	// values negligible.
	idLen = 16

	// Marker is the constant leading substring of every token. The streaming
	// re-hydrator uses it to detect a (possibly partial) token sitting at a
	// chunk boundary.
	Marker = prefix

	// MaxLen is a generous upper bound on the byte length of a token. The
	// streaming re-hydrator uses it to cap how many bytes it will hold back
	// while waiting for an unterminated marker to close.
	MaxLen = 128
)

var nonEntityChars = regexp.MustCompile(`[^A-Z0-9_]+`)

// Pattern matches a complete, well-formed token. The whole match is what gets
// replaced on the response path.
var Pattern = regexp.MustCompile(`\[\[CMPL_[A-Z0-9_]+_[0-9a-f]{16}\]\]`)

// Make returns the deterministic token for (entityType, value). The same
// (entityType, value, salt) triple always yields the same token.
func Make(entityType, value, salt string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + value))
	id := hex.EncodeToString(sum[:])[:idLen]
	return prefix + normalizeEntity(entityType) + "_" + id + suffix
}

// normalizeEntity upper-cases the entity type and replaces any character that
// is not part of the token alphabet so the result is always matchable by
// Pattern.
func normalizeEntity(e string) string {
	e = strings.ToUpper(strings.TrimSpace(e))
	e = nonEntityChars.ReplaceAllString(e, "_")
	e = strings.Trim(e, "_")
	if e == "" {
		return "PII"
	}
	return e
}
