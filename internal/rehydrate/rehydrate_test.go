package rehydrate

import (
	"context"
	"testing"

	"github.com/coreoptimizer/promptcloak/internal/tokenize"
)

// mapVault is a trivial in-test Vault.
type mapVault map[string]string

func (m mapVault) Get(_ context.Context, token string) (string, bool, error) {
	v, ok := m[token]
	return v, ok, nil
}

// feed pushes a payload through the rehydrator one chunk at a time and returns
// the concatenated output.
func feed(t *testing.T, r *Rehydrator, payload []byte, chunkSize int) string {
	t.Helper()
	var out []byte
	for i := 0; i < len(payload); i += chunkSize {
		end := i + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		eos := end == len(payload)
		got, err := r.Process(context.Background(), payload[i:end], eos)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		out = append(out, got...)
	}
	return string(out)
}

func TestRehydrateWholeAndSplit(t *testing.T) {
	tok := tokenize.Make("PERSON", "Jane Doe", "s")
	v := mapVault{tok: "Jane Doe"}
	payload := []byte("Hello " + tok + ", how are you?")
	want := "Hello Jane Doe, how are you?"

	// Try every chunk size: this stresses the boundary-spanning carry logic
	// since the token will be split at every possible offset.
	for cs := 1; cs <= len(payload); cs++ {
		r := New(v)
		got := feed(t, r, payload, cs)
		if got != want {
			t.Fatalf("chunkSize=%d: got %q want %q", cs, got, want)
		}
	}
}

func TestRehydrateMultipleTokens(t *testing.T) {
	t1 := tokenize.Make("PERSON", "Jane", "s")
	t2 := tokenize.Make("EMAIL_ADDRESS", "jane@x.com", "s")
	v := mapVault{t1: "Jane", t2: "jane@x.com"}
	payload := []byte("data: {\"text\":\"" + t1 + " <" + t2 + ">\"}\n\n")
	want := "data: {\"text\":\"Jane <jane@x.com>\"}\n\n"

	for cs := 1; cs <= len(payload); cs++ {
		r := New(v)
		if got := feed(t, r, payload, cs); got != want {
			t.Fatalf("chunkSize=%d: got %q want %q", cs, got, want)
		}
	}
}

func TestUnknownTokenLeftIntact(t *testing.T) {
	tok := tokenize.Make("PERSON", "Ghost", "s") // not in vault
	v := mapVault{}
	payload := []byte("x " + tok + " y")
	r := New(v)
	got := feed(t, r, payload, 3)
	if got != "x "+tok+" y" {
		t.Fatalf("unknown token should be left intact, got %q", got)
	}
}

func TestNoTokenPassthrough(t *testing.T) {
	v := mapVault{}
	payload := []byte("just some [[plain]] text with [[1,2]] arrays")
	r := New(v)
	if got := feed(t, r, payload, 4); got != string(payload) {
		t.Fatalf("passthrough mismatch: got %q want %q", got, payload)
	}
}
