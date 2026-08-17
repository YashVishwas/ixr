package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// decodedContentBlock mirrors contentBlock for test decoding.
type decodedContentBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type,omitempty"`
		Data      string `json:"data,omitempty"`
		URL       string `json:"url,omitempty"`
	} `json:"source,omitempty"`
}

type decodedMessage struct {
	Role    string                `json:"role"`
	Content []decodedContentBlock `json:"content"`
}

type decodedBody struct {
	System   string           `json:"system"`
	Messages []decodedMessage `json:"messages"`
}

// decodeBody is a shared helper across this file's tests.
func decodeBody(t *testing.T, body []byte) decodedBody {
	t.Helper()
	var decoded decodedBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("buildBody produced invalid JSON: %v", err)
	}
	return decoded
}

// TestBuildBody_PlainTextMessageStillSingleTextBlock confirms the
// pre-existing plain-text shape (a single text content block) is unchanged
// for a message with no Parts.
func TestBuildBody_PlainTextMessageStillSingleTextBlock(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{{Role: "user", Content: "hello"}},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded := decodeBody(t, body)

	if len(decoded.Messages) != 1 {
		t.Fatalf("messages: got %d, want 1", len(decoded.Messages))
	}
	blocks := decoded.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "hello" {
		t.Errorf("expected a single text block, got %+v", blocks)
	}
}

// TestBuildBody_ImageContentTranslated is the regression test for RFC Gap
// 10's remaining half: this adapter used to silently drop image content
// (m.Parts was never translated, only the already-flattened text
// survived). Now it must produce a real image content block, the same
// shape as native Anthropic's adapter, since Bedrock's Claude endpoint
// uses the identical wire format.
func TestBuildBody_ImageContentTranslated(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{
			{
				Role:    "user",
				Content: "what is in this image?", // flattened by pkg/schema's UnmarshalJSON
				Parts: []schema.ContentPart{
					{Type: "text", Text: "what is in this image?"},
					{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "data:image/png;base64,AAAABBBB"}},
				},
			},
		},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded := decodeBody(t, body)

	if len(decoded.Messages) != 1 {
		t.Fatalf("messages: got %d, want 1", len(decoded.Messages))
	}
	blocks := decoded.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("content blocks: got %d, want 2 (text + image)", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "what is in this image?" {
		t.Errorf("text block: got %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("image block: got %+v", blocks[1])
	}
	if blocks[1].Source.Type != "base64" || blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "AAAABBBB" {
		t.Errorf("image source: got %+v", blocks[1].Source)
	}
}

// TestBuildBody_ImageHTTPURLPassedThrough confirms a non-data: image URL is
// sent as a "url" source (Bedrock fetches it itself), matching native
// Anthropic's adapter.
func TestBuildBody_ImageHTTPURLPassedThrough(t *testing.T) {
	a := New("us-east-1", "AKIA...", "secret", "")
	req := &schema.RequestEnvelope{
		Model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []schema.Message{
			{Role: "user", Parts: []schema.ContentPart{
				{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "https://example.com/cat.png"}},
			}},
		},
	}

	body, err := a.buildBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded := decodeBody(t, body)

	blocks := decoded.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "image" || blocks[0].Source == nil {
		t.Fatalf("image block: got %+v", blocks)
	}
	if blocks[0].Source.Type != "url" || blocks[0].Source.URL != "https://example.com/cat.png" {
		t.Errorf("image source: got %+v, want a url-type source (Bedrock fetches it)", blocks[0].Source)
	}
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
