package redact

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coreoptimizer/promptcloak/internal/detect"
	"github.com/coreoptimizer/promptcloak/internal/rehydrate"
	"github.com/coreoptimizer/promptcloak/internal/tokenize"
	"github.com/coreoptimizer/promptcloak/internal/vault"
)

// fakeAnalyzer returns findings for fixed substrings, computing rune offsets.
type fakeAnalyzer struct {
	spans map[string]string // substring -> entity type
}

func (f fakeAnalyzer) Analyze(_ context.Context, text, _ string) ([]detect.Finding, error) {
	runes := []rune(text)
	var out []detect.Finding
	for sub, entity := range f.spans {
		subR := []rune(sub)
		for i := 0; i+len(subR) <= len(runes); i++ {
			if string(runes[i:i+len(subR)]) == sub {
				out = append(out, detect.Finding{EntityType: entity, Start: i, End: i + len(subR), Score: 0.9})
			}
		}
	}
	return out, nil
}

func TestTransformTokenizesAndStores(t *testing.T) {
	v := vault.NewMemory(time.Minute)
	defer v.Close()
	a := fakeAnalyzer{spans: map[string]string{"Jane Doe": "PERSON", "jane@x.com": "EMAIL_ADDRESS"}}
	r := New(a, v, "en", "salt")

	in := "Contact Jane Doe at jane@x.com please"
	out, err := r.Transform(context.Background(), in)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if strings.Contains(out, "Jane Doe") || strings.Contains(out, "jane@x.com") {
		t.Fatalf("raw PII leaked into output: %q", out)
	}
	if !tokenize.Pattern.MatchString(out) {
		t.Fatalf("expected tokens in output: %q", out)
	}

	// Round-trip: re-hydrating the redacted text restores the original.
	rh := rehydrate.New(v)
	restored, err := rh.Process(context.Background(), []byte(out), true)
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	if string(restored) != in {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", restored, in)
	}
}

func TestTransformNoFindings(t *testing.T) {
	v := vault.NewMemory(time.Minute)
	defer v.Close()
	r := New(fakeAnalyzer{spans: map[string]string{}}, v, "en", "salt")
	in := "nothing sensitive here"
	out, err := r.Transform(context.Background(), in)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out != in {
		t.Fatalf("expected passthrough, got %q", out)
	}
}

func TestTransformUnicodeOffsets(t *testing.T) {
	v := vault.NewMemory(time.Minute)
	defer v.Close()
	// Leading multi-byte characters shift byte offsets but not rune offsets.
	a := fakeAnalyzer{spans: map[string]string{"José": "PERSON"}}
	r := New(a, v, "en", "salt")
	in := "café owner José waved 🙂"
	out, err := r.Transform(context.Background(), in)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if strings.Contains(out, "José") {
		t.Fatalf("name not redacted (offset bug?): %q", out)
	}
	if !strings.Contains(out, "café owner ") || !strings.Contains(out, " waved 🙂") {
		t.Fatalf("surrounding text corrupted: %q", out)
	}
}

func TestSelectSpansOverlap(t *testing.T) {
	// Overlapping findings: keep the longer one starting earliest.
	findings := []detect.Finding{
		{Start: 0, End: 5, Score: 0.8},
		{Start: 2, End: 9, Score: 0.9}, // overlaps the first
		{Start: 9, End: 12, Score: 0.7},
	}
	got := selectSpans(findings, 20)
	if len(got) != 2 {
		t.Fatalf("expected 2 non-overlapping spans, got %d: %+v", len(got), got)
	}
	if got[0].Start != 0 || got[0].End != 5 {
		t.Errorf("unexpected first span: %+v", got[0])
	}
	if got[1].Start != 9 || got[1].End != 12 {
		t.Errorf("unexpected second span: %+v", got[1])
	}
}
