// Package redact ties detection, tokenization and the vault together: given a
// piece of text it detects PII, replaces each span with a reversible token, and
// persists the token->value mapping so the response path can restore it.
package redact

import (
	"context"
	"sort"
	"strings"

	"github.com/coreoptimizer/promptcloak/internal/detect"
	"github.com/coreoptimizer/promptcloak/internal/tokenize"
	"github.com/coreoptimizer/promptcloak/internal/vault"
)

// Redactor implements llmbody.Transformer.
type Redactor struct {
	analyzer detect.Analyzer
	vault    vault.Vault
	language string
	salt     string
}

// New builds a Redactor.
func New(a detect.Analyzer, v vault.Vault, language, salt string) *Redactor {
	return &Redactor{analyzer: a, vault: v, language: language, salt: salt}
}

// Transform detects PII in text and returns a copy with each detected span
// replaced by a reversible token. Detected values are stored in the vault. Text
// with no detections is returned unchanged.
func (r *Redactor) Transform(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	findings, err := r.analyzer.Analyze(ctx, text, r.language)
	if err != nil {
		return "", err
	}
	if len(findings) == 0 {
		return text, nil
	}

	// Presidio reports rune (code-point) offsets; slice on runes so non-ASCII
	// text is handled correctly.
	runes := []rune(text)
	spans := selectSpans(findings, len(runes))
	if len(spans) == 0 {
		return text, nil
	}

	var b strings.Builder
	last := 0
	for _, f := range spans {
		b.WriteString(string(runes[last:f.Start]))
		value := string(runes[f.Start:f.End])
		token := tokenize.Make(f.EntityType, value, r.salt)
		if err := r.vault.Put(ctx, token, value); err != nil {
			return "", err
		}
		b.WriteString(token)
		last = f.End
	}
	b.WriteString(string(runes[last:]))
	return b.String(), nil
}

// selectSpans validates findings against the text length and resolves overlaps:
// it keeps the longest (then highest-scoring) span starting earliest and drops
// any span that overlaps an already-selected one. The returned spans are sorted
// by Start and are non-overlapping.
func selectSpans(findings []detect.Finding, n int) []detect.Finding {
	valid := make([]detect.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Start >= 0 && f.End <= n && f.Start < f.End {
			valid = append(valid, f)
		}
	}

	sort.Slice(valid, func(i, j int) bool {
		a, b := valid[i], valid[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End // prefer the longer span
		}
		return a.Score > b.Score
	})

	selected := make([]detect.Finding, 0, len(valid))
	lastEnd := 0
	for _, f := range valid {
		if f.Start >= lastEnd {
			selected = append(selected, f)
			lastEnd = f.End
		}
	}
	return selected
}
