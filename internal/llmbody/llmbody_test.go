package llmbody

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// upperTransformer upper-cases text so we can assert which fields were touched.
type upperTransformer struct{}

func (upperTransformer) Transform(_ context.Context, text string) (string, error) {
	return strings.ToUpper(text), nil
}

func sanitize(t *testing.T, in string) map[string]any {
	t.Helper()
	out, err := SanitizeRequest(context.Background(), []byte(in), upperTransformer{})
	if err != nil {
		t.Fatalf("SanitizeRequest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("result not JSON: %v (%s)", err, out)
	}
	return m
}

func TestOpenAIChatStringContent(t *testing.T) {
	m := sanitize(t, `{"model":"gpt-4","messages":[{"role":"user","content":"hello"},{"role":"system","content":"be nice"}]}`)
	if m["model"] != "gpt-4" {
		t.Errorf("model should be untouched, got %v", m["model"])
	}
	msgs := m["messages"].([]any)
	if got := msgs[0].(map[string]any)["content"]; got != "HELLO" {
		t.Errorf("user content not transformed: %v", got)
	}
	if got := msgs[1].(map[string]any)["content"]; got != "BE NICE" {
		t.Errorf("system message content not transformed: %v", got)
	}
	if got := msgs[0].(map[string]any)["role"]; got != "user" {
		t.Errorf("role should be untouched: %v", got)
	}
}

func TestOpenAIMultimodalParts(t *testing.T) {
	m := sanitize(t, `{"messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"http://x/y.png"}}]}]}`)
	parts := m["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if got := parts[0].(map[string]any)["text"]; got != "LOOK" {
		t.Errorf("text part not transformed: %v", got)
	}
	img := parts[1].(map[string]any)["image_url"].(map[string]any)
	if img["url"] != "http://x/y.png" {
		t.Errorf("image url should be untouched: %v", img["url"])
	}
}

func TestAnthropicSystemAndBlocks(t *testing.T) {
	m := sanitize(t, `{"system":"sys prompt","messages":[{"role":"user","content":[{"type":"text","text":"hi there"}]}]}`)
	if m["system"] != "SYS PROMPT" {
		t.Errorf("system not transformed: %v", m["system"])
	}
	block := m["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if block["text"] != "HI THERE" {
		t.Errorf("content block not transformed: %v", block["text"])
	}
}

func TestNonJSONPassthrough(t *testing.T) {
	in := []byte("not json at all")
	out, err := SanitizeRequest(context.Background(), in, upperTransformer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("non-JSON body should pass through unchanged, got %q", out)
	}
}

func TestUnchangedBodyReturnedVerbatim(t *testing.T) {
	// No text-bearing fields -> bytes returned verbatim.
	in := []byte(`{"model":"gpt-4","temperature":0.5}`)
	out, err := SanitizeRequest(context.Background(), in, upperTransformer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("unchanged body should be byte-identical, got %q", out)
	}
}
