package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// TestBuildBody_ImageContentDroppedNotErroredOrCorrupted locks in the
// documented trade-off for RFC Gap 10: this adapter doesn't translate
// vision content yet, but a message carrying it must still produce a
// valid, text-only request body (using the already-flattened m.Content)
// rather than erroring or sending a malformed body — see the slog.Warn
// next to this loop in adapter.go for the operator-visibility half of the
// fix.
func TestBuildBody_ImageContentDroppedNotErroredOrCorrupted(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{
			{
				Role:    "user",
				Content: "what is in this image?", // flattened by pkg/schema's UnmarshalJSON
				Parts: []schema.ContentPart{
					{Type: "text", Text: "what is in this image?"},
					{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "data:image/png;base64,AAAA"}},
				},
			},
		},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("buildBody produced invalid JSON: %v", err)
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0].Content != "what is in this image?" {
		t.Errorf("expected the flattened text to be forwarded, got %+v", decoded.Messages)
	}
}

// decodeBody is a shared helper for the system-message tests below.
func decodeBody(t *testing.T, body []byte) struct {
	System   string `json:"system"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
} {
	t.Helper()
	var decoded struct {
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("buildBody produced invalid JSON: %v", err)
	}
	return decoded
}

// TestBuildBody_SystemMessageLiftedToTopLevel is the regression test for
// the bug this fix closes: buildBody used to unconditionally `continue`
// past every role=="system" message with no System field to lift it into
// at all — every system message was dropped, always, for every Claude
// model called via Bedrock. Worse than the equivalent bug already found
// and fixed in the native Anthropic adapter (which only lost all but the
// last of multiple system messages) — this lost the only one too.
func TestBuildBody_SystemMessageLiftedToTopLevel(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
		},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded := decodeBody(t, body)

	if decoded.System != "be concise" {
		t.Errorf("system: got %q, want %q", decoded.System, "be concise")
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0].Role != "user" {
		t.Errorf("messages: expected only the user message (system removed), got %+v", decoded.Messages)
	}
}

// TestBuildBody_MultipleSystemMessagesConcatenated confirms multiple
// system-role messages (e.g. MemoryMiddleware's injected user-facts system
// message ahead of the caller's own one) are concatenated in order, not
// just the last one kept — the same fix already applied to the native
// Anthropic adapter.
func TestBuildBody_MultipleSystemMessagesConcatenated(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{
			{Role: "system", Content: "What you know about this user: User's name is Arun"},
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
		},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded := decodeBody(t, body)

	want := "What you know about this user: User's name is Arun\n\nbe concise"
	if decoded.System != want {
		t.Errorf("system: got %q, want %q", decoded.System, want)
	}
	if len(decoded.Messages) != 1 {
		t.Errorf("messages: got %d, want 1 (both system messages removed)", len(decoded.Messages))
	}
}

// TestBuildBody_NoSystemMessage_OmitsSystemField confirms a request with no
// system message at all produces no "system" key (omitempty), rather than
// an empty string — matching the native Anthropic adapter's shape and
// avoiding sending a field Bedrock's Claude endpoint doesn't expect for a
// systemless request.
func TestBuildBody_NoSystemMessage_OmitsSystemField(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{{Role: "user", Content: "hello"}},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("buildBody produced invalid JSON: %v", err)
	}
	if _, present := raw["system"]; present {
		t.Errorf("expected no \"system\" key when there's no system message, got %v", raw["system"])
	}
}
