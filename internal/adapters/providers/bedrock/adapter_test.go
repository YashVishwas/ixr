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
