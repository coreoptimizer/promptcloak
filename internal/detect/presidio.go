package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Presidio is an Analyzer backed by a Presidio analyzer REST service. Presidio
// already reports code-point (rune) offsets and uses the canonical entity
// vocabulary, so no conversion or normalization is needed here.
//
// See https://microsoft.github.io/presidio/analyzer/ — POST /analyze with a
// JSON body of {text, language, score_threshold, entities} returns a JSON array
// of {entity_type, start, end, score}.
type Presidio struct {
	baseURL   string
	client    *http.Client
	threshold float64
	entities  []string
}

// NewPresidio builds a Presidio client. entities may be nil to detect all
// supported entity types.
func NewPresidio(baseURL string, threshold float64, entities []string, timeout time.Duration) *Presidio {
	return &Presidio{
		baseURL:   baseURL,
		client:    &http.Client{Timeout: timeout},
		threshold: threshold,
		entities:  entities,
	}
}

type analyzeRequest struct {
	Text           string   `json:"text"`
	Language       string   `json:"language"`
	ScoreThreshold float64  `json:"score_threshold,omitempty"`
	Entities       []string `json:"entities,omitempty"`
}

type analyzeResult struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

func (p *Presidio) Analyze(ctx context.Context, text, language string) ([]Finding, error) {
	body, err := json.Marshal(analyzeRequest{
		Text:           text,
		Language:       language,
		ScoreThreshold: p.threshold,
		Entities:       p.entities,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("presidio analyze: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("presidio analyze: status %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
	}

	var results []analyzeResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("presidio analyze: decode: %w", err)
	}

	findings := make([]Finding, 0, len(results))
	for _, r := range results {
		findings = append(findings, Finding{
			EntityType: r.EntityType,
			Start:      r.Start,
			End:        r.End,
			Score:      r.Score,
		})
	}
	return findings, nil
}
