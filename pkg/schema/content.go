package schema

import (
	"encoding/json"
	"fmt"
)

// ContentPart is one block of a multi-part message ("content" as an array
// instead of a bare string) — the shape OpenAI- and Anthropic-compatible
// APIs use for messages that mix text and images.
type ContentPart struct {
	Type     string        `json:"type"` // "text" | "image_url"
	Text     string        `json:"text,omitempty"`
	ImageURL *ImageURLPart `json:"image_url,omitempty"`
}

// ImageURLPart is an image reference: an https:// URL, or a data: URI
// carrying inline base64 image bytes.
type ImageURLPart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto" | "low" | "high"
}

// MarshalJSON emits Parts as the "content" array when present, otherwise
// falls back to the plain string Content field — so a text-only message
// serializes exactly as it always has.
func (m Message) MarshalJSON() ([]byte, error) {
	type alias Message
	if len(m.Parts) == 0 {
		return json.Marshal(alias(m))
	}
	return json.Marshal(struct {
		alias
		Content []ContentPart `json:"content"`
	}{alias: alias(m), Content: m.Parts})
}

// UnmarshalJSON detects whether "content" arrived as a bare string (the
// existing shape, left untouched) or an array of content parts
// (multimodal), and populates Content or Parts accordingly.
func (m *Message) UnmarshalJSON(b []byte) error {
	type alias Message
	var probe struct {
		alias
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	*m = Message(probe.alias)

	if len(probe.Content) == 0 {
		return nil
	}
	switch probe.Content[0] {
	case '"':
		var s string
		if err := json.Unmarshal(probe.Content, &s); err != nil {
			return err
		}
		m.Content = s
	case '[':
		var parts []ContentPart
		if err := json.Unmarshal(probe.Content, &parts); err != nil {
			return err
		}
		m.Parts = parts
		// Flatten the text parts into Content too, so code that only reads
		// .Content (memory extraction, session-history trimming, cache
		// keys) still sees something reasonable instead of an empty
		// string on a multimodal message.
		var text string
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				if text != "" {
					text += "\n"
				}
				text += p.Text
			}
		}
		m.Content = text
	case 'n': // JSON null — leave Content/Parts unset
	default:
		return fmt.Errorf("schema: message content must be a string or an array of content parts, got %q", probe.Content)
	}
	return nil
}
