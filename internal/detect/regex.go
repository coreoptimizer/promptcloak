package detect

import (
	"context"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

// Regex is a dependency-free Analyzer that matches a fixed set of well-formed
// PII patterns. It needs no external service, which makes it useful for local
// development, air-gapped deployments, and tests. It is necessarily less
// capable than ML-based backends: it cannot find free-form entities such as
// PERSON or LOCATION, only structurally-regular ones (emails, SSNs, etc.).
type Regex struct {
	patterns []regexPattern
	timeout  time.Duration
}

type regexPattern struct {
	entity string
	re     *regexp.Regexp
}

// defaultPatterns are the structurally-regular entities the regex backend can
// recognize. Patterns are intentionally conservative to limit false positives.
// RE2 (Go's regexp) has no backreferences but supports word boundaries.
var defaultPatterns = []struct {
	entity, expr string
}{
	{EntityEmail, `[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`},
	{EntityUSSSN, `\b\d{3}-\d{2}-\d{4}\b`},
	{EntityCreditCard, `\b\d{4}[ \-]?\d{4}[ \-]?\d{4}[ \-]?\d{4}\b`},
	{EntityPhone, `\b(?:\+?\d{1,3}[ .\-]?)?(?:\(\d{3}\)|\d{3})[ .\-]\d{3}[ .\-]\d{4}\b`},
	{EntityIPAddress, `\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`},
	{EntityURL, `\bhttps?://[^\s]+`},
}

// NewRegex builds the regex backend, restricted to entities (canonical names)
// when the allowlist is non-empty. timeout is accepted for signature parity
// with the remote backends; the regex backend is purely local and ignores it.
func NewRegex(entities []string, timeout time.Duration) (*Regex, error) {
	set := allowSet(entities)
	r := &Regex{timeout: timeout}
	for _, p := range defaultPatterns {
		if !allowed(set, p.entity) {
			continue
		}
		re, err := regexp.Compile(p.expr)
		if err != nil {
			return nil, err
		}
		r.patterns = append(r.patterns, regexPattern{entity: p.entity, re: re})
	}
	return r, nil
}

// Analyze scans text for each configured pattern. The language argument is
// ignored — regex patterns are language-independent.
func (r *Regex) Analyze(_ context.Context, text, _ string) ([]Finding, error) {
	// regexp reports byte offsets; build a byte->rune index once so every match
	// can be reported as the rune offsets the redactor expects.
	byteToRune := runeOffsetIndex(text)

	var findings []Finding
	for _, p := range r.patterns {
		for _, loc := range p.re.FindAllStringIndex(text, -1) {
			findings = append(findings, Finding{
				EntityType: p.entity,
				Start:      byteToRune[loc[0]],
				End:        byteToRune[loc[1]],
				Score:      1.0, // deterministic match
			})
		}
	}
	// Stable order keeps output deterministic across pattern iteration.
	sort.Slice(findings, func(i, j int) bool { return findings[i].Start < findings[j].Start })
	return findings, nil
}

// runeOffsetIndex maps every byte offset in text (0..len(text)) to its rune
// index in a single pass, so byte offsets returned by regexp can be converted
// to the rune offsets the redactor slices on. Continuation bytes are mapped to
// the index of the rune they belong to; match boundaries for the patterns here
// always fall on rune starts.
func runeOffsetIndex(text string) []int {
	idx := make([]int, len(text)+1)
	runeCount := 0
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		for j := 0; j < size; j++ {
			idx[i+j] = runeCount
		}
		i += size
		runeCount++
	}
	idx[len(text)] = runeCount
	return idx
}
