// Package rehydrate restores real values on the response path by swapping
// compliance tokens back to the values stored in the vault.
//
// It is streaming-aware: model responses arrive as a sequence of chunks (e.g.
// SSE events), and a token may straddle a chunk boundary. The Rehydrator holds
// back the minimal trailing bytes that could be the start of a token until the
// next chunk arrives, so boundary-spanning tokens are still matched.
package rehydrate

import (
	"bytes"
	"context"

	"github.com/coreoptimizer/promptcloak/internal/tokenize"
)

// Vault is the read side of the token store.
type Vault interface {
	Get(ctx context.Context, token string) (value string, ok bool, err error)
}

// Rehydrator is stateful and must be used for exactly one response stream. It is
// not safe for concurrent use.
type Rehydrator struct {
	v     Vault
	carry []byte
}

// New returns a Rehydrator reading from v.
func New(v Vault) *Rehydrator {
	return &Rehydrator{v: v}
}

// Process re-hydrates one chunk. It prepends any bytes held back from the
// previous call, swaps complete tokens for their values, and (unless eos)
// holds back a trailing partial token for the next call. The returned slice is
// the bytes ready to forward downstream.
func (r *Rehydrator) Process(ctx context.Context, chunk []byte, eos bool) ([]byte, error) {
	buf := make([]byte, 0, len(r.carry)+len(chunk))
	buf = append(buf, r.carry...)
	buf = append(buf, chunk...)
	r.carry = nil

	out, err := r.replaceAll(ctx, buf)
	if err != nil {
		return nil, err
	}

	if eos {
		// Flush everything; any unterminated marker is emitted as-is.
		return out, nil
	}

	if hf := partialStart(out); hf >= 0 {
		r.carry = append([]byte(nil), out[hf:]...)
		out = out[:hf]
	}
	return out, nil
}

// replaceAll replaces every complete token in buf with its vault value. Unknown
// tokens (no vault entry) are left untouched.
func (r *Rehydrator) replaceAll(ctx context.Context, buf []byte) ([]byte, error) {
	locs := tokenize.Pattern.FindAllIndex(buf, -1)
	if locs == nil {
		return buf, nil
	}
	var out bytes.Buffer
	last := 0
	for _, loc := range locs {
		out.Write(buf[last:loc[0]])
		token := string(buf[loc[0]:loc[1]])
		value, ok, err := r.v.Get(ctx, token)
		if err != nil {
			return nil, err
		}
		if ok {
			out.WriteString(value)
		} else {
			out.Write(buf[loc[0]:loc[1]]) // leave unknown tokens verbatim
		}
		last = loc[1]
	}
	out.Write(buf[last:])
	return out.Bytes(), nil
}

// partialStart returns the index at which a possible incomplete token begins, or
// -1 if the buffer has no trailing partial token. By the time this runs all
// complete tokens have already been replaced, so any remaining marker is
// necessarily unterminated.
func partialStart(b []byte) int {
	marker := []byte(tokenize.Marker)

	// Case 1: a full marker is present but not yet closed by "]]".
	if k := bytes.LastIndex(b, marker); k >= 0 {
		if !bytes.Contains(b[k:], []byte("]]")) && len(b)-k <= tokenize.MaxLen {
			return k
		}
	}

	// Case 2: the buffer ends with a proper prefix of the marker (e.g. "[[CM").
	for n := len(marker) - 1; n >= 1; n-- {
		if bytes.HasSuffix(b, marker[:n]) {
			return len(b) - n
		}
	}
	return -1
}
