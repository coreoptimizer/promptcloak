// Package detect finds PII spans in text behind a small, provider-agnostic
// interface. The project ships three interchangeable backends — Microsoft
// Presidio (the default), a dependency-free built-in regex matcher, and Google
// Cloud DLP — selected at startup via the DETECTOR setting. Adding another
// provider means implementing Analyzer and registering it in New; nothing
// downstream (tokenization, the vault, re-hydration) needs to change.
package detect

import (
	"context"
	"fmt"
	"time"
)

// Finding is a single detected entity span.
//
// Start and End are code-point (rune) offsets into the analyzed text, NOT byte
// offsets. The redactor slices the text on runes, so every backend MUST report
// rune offsets — adapters for providers that return UTF-8 byte offsets are
// responsible for converting. EntityType uses the canonical (Presidio-style)
// vocabulary — see entities.go — so token labels are stable across backends.
type Finding struct {
	EntityType string
	Start      int
	End        int
	Score      float64
}

// Analyzer detects PII spans in a piece of text. It is the single seam every
// detection backend implements.
type Analyzer interface {
	Analyze(ctx context.Context, text, language string) ([]Finding, error)
}

// Backend names accepted by the DETECTOR setting.
const (
	BackendPresidio = "presidio"
	BackendRegex    = "regex"
	BackendGCPDLP   = "gcpdlp"
)

// Options is the fully-resolved detection configuration. config.Load populates
// it from the environment; New turns it into a concrete Analyzer.
type Options struct {
	// Backend selects the implementation: one of the Backend* constants.
	Backend string
	// Language is the detection language (e.g. "en"). Forwarded to backends
	// that support it; ignored by the regex backend.
	Language string
	// Entities is the canonical entity allowlist. Empty means "all entities the
	// backend supports".
	Entities []string
	// ScoreThreshold is the minimum confidence [0,1] to act on a span.
	ScoreThreshold float64
	// Timeout bounds a single detection call to a remote backend.
	Timeout time.Duration

	// PresidioURL is the analyzer base URL (BackendPresidio only).
	PresidioURL string
	// GCPDLP holds the Google Cloud DLP settings (BackendGCPDLP only).
	GCPDLP GCPDLPOptions
}

// GCPDLPOptions configures the Google Cloud DLP backend.
type GCPDLPOptions struct {
	// Project is the GCP project id that owns the DLP request (required).
	Project string
	// Location is the DLP processing location, e.g. "global".
	Location string
	// MinLikelihood is the DLP likelihood floor (e.g. "POSSIBLE", "LIKELY").
	MinLikelihood string
	// Token is a static OAuth2 access token. When empty, the backend fetches a
	// token from the GCE/GKE metadata server (workload identity).
	Token string
	// Endpoint overrides the DLP API base URL (default https://dlp.googleapis.com).
	Endpoint string
}

// New builds the Analyzer selected by o.Backend.
func New(o Options) (Analyzer, error) {
	switch o.Backend {
	case BackendPresidio, "":
		return NewPresidio(o.PresidioURL, o.ScoreThreshold, o.Entities, o.Timeout), nil
	case BackendRegex:
		return NewRegex(o.Entities, o.Timeout)
	case BackendGCPDLP:
		return NewGCPDLP(o.GCPDLP, o.Entities, o.ScoreThreshold, o.Timeout)
	default:
		return nil, fmt.Errorf("unknown DETECTOR %q (want %q, %q or %q)",
			o.Backend, BackendPresidio, BackendRegex, BackendGCPDLP)
	}
}
