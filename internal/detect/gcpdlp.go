package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GCPDLP is an Analyzer backed by Google Cloud Data Loss Prevention's
// content:inspect REST API. DLP reports code-point ranges directly, so its
// offsets map onto our rune-offset contract without conversion. DLP info-type
// names are normalized to the canonical vocabulary so token labels stay
// consistent with the other backends.
//
// Auth: a static OAuth2 access token (GCPDLPOptions.Token) is used if set;
// otherwise a token is fetched from the GCE/GKE metadata server, which is the
// workload-identity path in-cluster.
type GCPDLP struct {
	project       string
	location      string
	minLikelihood string
	endpoint      string
	infoTypes     []dlpInfoType // canonical allowlist mapped to DLP names; nil = DLP defaults
	threshold     float64
	client        *http.Client
	tokens        *tokenSource
}

// NewGCPDLP builds the DLP backend. project must be set.
func NewGCPDLP(o GCPDLPOptions, entities []string, threshold float64, timeout time.Duration) (*GCPDLP, error) {
	if strings.TrimSpace(o.Project) == "" {
		return nil, fmt.Errorf("gcpdlp: GCP_DLP_PROJECT must be set")
	}
	location := o.Location
	if location == "" {
		location = "global"
	}
	minLikelihood := o.MinLikelihood
	if minLikelihood == "" {
		minLikelihood = "POSSIBLE"
	}
	endpoint := strings.TrimRight(o.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://dlp.googleapis.com"
	}
	client := &http.Client{Timeout: timeout}

	var infoTypes []dlpInfoType
	for _, e := range entities { // canonical allowlist -> DLP info types
		if name, ok := canonicalToDLP[e]; ok {
			infoTypes = append(infoTypes, dlpInfoType{Name: name})
		}
	}

	return &GCPDLP{
		project:       o.Project,
		location:      location,
		minLikelihood: minLikelihood,
		endpoint:      endpoint,
		infoTypes:     infoTypes,
		threshold:     threshold,
		client:        client,
		tokens:        &tokenSource{static: o.Token, client: client},
	}, nil
}

func (g *GCPDLP) Analyze(ctx context.Context, text, _ string) ([]Finding, error) {
	reqBody, err := json.Marshal(dlpInspectRequest{
		Item: dlpContentItem{Value: text},
		InspectConfig: dlpInspectConfig{
			InfoTypes:     g.infoTypes,
			MinLikelihood: g.minLikelihood,
			IncludeQuote:  false,
		},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v2/projects/%s/locations/%s/content:inspect", g.endpoint, g.project, g.location)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	token, err := g.tokens.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcpdlp: auth: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcpdlp inspect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("gcpdlp inspect: status %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
	}

	var out dlpInspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gcpdlp inspect: decode: %w", err)
	}

	findings := make([]Finding, 0, len(out.Result.Findings))
	for _, f := range out.Result.Findings {
		score := likelihoodScore(f.Likelihood)
		if score < g.threshold {
			continue
		}
		findings = append(findings, Finding{
			EntityType: dlpToCanonical(f.InfoType.Name),
			Start:      int(f.Location.CodepointRange.Start),
			End:        int(f.Location.CodepointRange.End),
			Score:      score,
		})
	}
	return findings, nil
}

// canonicalToDLP maps canonical entity names to DLP info-type names for the
// request; dlpToCanonical reverses it for findings (falling back to the DLP
// name when unmapped, which is still a valid token label).
var canonicalToDLP = map[string]string{
	EntityPerson:     "PERSON_NAME",
	EntityEmail:      "EMAIL_ADDRESS",
	EntityPhone:      "PHONE_NUMBER",
	EntityUSSSN:      "US_SOCIAL_SECURITY_NUMBER",
	EntityCreditCard: "CREDIT_CARD_NUMBER",
	EntityIPAddress:  "IP_ADDRESS",
	EntityLocation:   "LOCATION",
	EntityURL:        "URL",
	EntityIBAN:       "IBAN_CODE",
}

func dlpToCanonical(dlpName string) string {
	for canon, dlp := range canonicalToDLP {
		if dlp == dlpName {
			return canon
		}
	}
	return dlpName
}

// likelihoodScore maps DLP's enum to a [0,1] score so the shared score
// threshold applies uniformly across backends.
func likelihoodScore(l string) float64 {
	switch l {
	case "VERY_LIKELY":
		return 0.9
	case "LIKELY":
		return 0.7
	case "POSSIBLE":
		return 0.5
	case "UNLIKELY":
		return 0.3
	case "VERY_UNLIKELY":
		return 0.1
	default:
		return 0.5
	}
}

// DLP request/response shapes (subset). int64 ranges are serialized as JSON
// strings by the protobuf JSON mapping, so flexInt accepts both forms.
type dlpInspectRequest struct {
	Item          dlpContentItem   `json:"item"`
	InspectConfig dlpInspectConfig `json:"inspectConfig"`
}

type dlpContentItem struct {
	Value string `json:"value"`
}

type dlpInspectConfig struct {
	InfoTypes     []dlpInfoType `json:"infoTypes,omitempty"`
	MinLikelihood string        `json:"minLikelihood,omitempty"`
	IncludeQuote  bool          `json:"includeQuote"`
}

type dlpInfoType struct {
	Name string `json:"name"`
}

type dlpInspectResponse struct {
	Result struct {
		Findings []struct {
			InfoType   dlpInfoType `json:"infoType"`
			Likelihood string      `json:"likelihood"`
			Location   struct {
				CodepointRange struct {
					Start flexInt `json:"start"`
					End   flexInt `json:"end"`
				} `json:"codepointRange"`
			} `json:"location"`
		} `json:"findings"`
	} `json:"result"`
}

// flexInt decodes an int that may arrive as a JSON number or a quoted string
// (protobuf's int64 JSON encoding uses strings).
type flexInt int

func (i *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*i = flexInt(n)
	return nil
}

// tokenSource yields an OAuth2 bearer token: either a static one or a
// metadata-server token cached until shortly before it expires.
type tokenSource struct {
	static string
	client *http.Client

	mu     sync.Mutex
	cached string
	expiry time.Time
}

const metadataTokenURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"

func (t *tokenSource) token(ctx context.Context) (string, error) {
	if t.static != "" {
		return t.static, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cached != "" && time.Now().Before(t.expiry) {
		return t.cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("metadata token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("metadata token: status %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
	}
	var md struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return "", fmt.Errorf("metadata token: decode: %w", err)
	}
	t.cached = md.AccessToken
	// Refresh a minute early to avoid using a token that expires mid-request.
	t.expiry = time.Now().Add(time.Duration(md.ExpiresIn-60) * time.Second)
	return t.cached, nil
}
