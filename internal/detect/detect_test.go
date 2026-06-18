package detect

import (
	"context"
	"testing"
	"time"
)

func TestNewSelectsBackend(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"default is presidio", Options{Backend: "", PresidioURL: "http://x"}, false},
		{"presidio", Options{Backend: BackendPresidio, PresidioURL: "http://x"}, false},
		{"regex", Options{Backend: BackendRegex}, false},
		{"gcpdlp needs project", Options{Backend: BackendGCPDLP}, true},
		{"gcpdlp ok", Options{Backend: BackendGCPDLP, GCPDLP: GCPDLPOptions{Project: "p"}}, false},
		{"unknown", Options{Backend: "bogus"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got analyzer %T", a)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a == nil {
				t.Fatal("expected analyzer, got nil")
			}
		})
	}
}

func TestRegexAnalyzeFindsAndOffsets(t *testing.T) {
	r, err := NewRegex(nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Leading multi-byte runes shift byte offsets but not rune offsets; the
	// rune offsets must slice the email out exactly.
	text := "café → mail jane@acme.com now"
	findings, err := r.Analyze(context.Background(), text, "en")
	if err != nil {
		t.Fatal(err)
	}
	var got *Finding
	for i := range findings {
		if findings[i].EntityType == EntityEmail {
			got = &findings[i]
		}
	}
	if got == nil {
		t.Fatalf("expected an EMAIL_ADDRESS finding, got %+v", findings)
	}
	runes := []rune(text)
	if string(runes[got.Start:got.End]) != "jane@acme.com" {
		t.Fatalf("offsets sliced %q, want %q", string(runes[got.Start:got.End]), "jane@acme.com")
	}
}

func TestRegexAllowlist(t *testing.T) {
	// Restrict to SSN only; an email in the text must be ignored.
	r, err := NewRegex([]string{EntityUSSSN}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := r.Analyze(context.Background(), "ssn 123-45-6789 mail a@b.com", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].EntityType != EntityUSSSN {
		t.Fatalf("allowlist not honored: %+v", findings)
	}
}

func TestDLPToCanonical(t *testing.T) {
	if got := dlpToCanonical("US_SOCIAL_SECURITY_NUMBER"); got != EntityUSSSN {
		t.Fatalf("US_SOCIAL_SECURITY_NUMBER -> %q, want %q", got, EntityUSSSN)
	}
	// Unmapped DLP types fall through unchanged (still a valid token label).
	if got := dlpToCanonical("PASSPORT"); got != "PASSPORT" {
		t.Fatalf("unmapped type changed: %q", got)
	}
}
