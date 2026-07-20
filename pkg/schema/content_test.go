package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessage_UnmarshalPlainStringContent_Unchanged(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"hello"}`), &m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Content != "hello" {
		t.Errorf("content: got %q, want hello", m.Content)
	}
	if m.Parts != nil {
		t.Errorf("parts: got %+v, want nil for a plain-string message", m.Parts)
	}
}

func TestMessage_MarshalPlainStringContent_Unchanged(t *testing.T) {
	m := Message{Role: "user", Content: "hello"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"role":"user","content":"hello"}`
	if string(b) != want {
		t.Errorf("marshal: got %s, want %s", b, want)
	}
}

func TestMessage_UnmarshalMultipartContent(t *testing.T) {
	raw := `{"role":"user","content":[
		{"type":"text","text":"what is in this image?"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA","detail":"high"}}
	]}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Parts) != 2 {
		t.Fatalf("parts: got %d, want 2", len(m.Parts))
	}
	if m.Parts[0].Type != "text" || m.Parts[0].Text != "what is in this image?" {
		t.Errorf("text part: got %+v", m.Parts[0])
	}
	if m.Parts[1].Type != "image_url" || m.Parts[1].ImageURL == nil || m.Parts[1].ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Errorf("image part: got %+v", m.Parts[1])
	}
	if m.Content != "what is in this image?" {
		t.Errorf("flattened content: got %q, want the text part's content", m.Content)
	}
}

func TestMessage_MarshalMultipartContent(t *testing.T) {
	m := Message{
		Role: "user",
		Parts: []ContentPart{
			{Type: "text", Text: "describe this"},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "https://example.com/cat.png"}},
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Round-trip back through Unmarshal rather than comparing raw bytes —
	// field order isn't a contract, content shape is.
	var got Message
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("round-trip unmarshal failed: %v", err)
	}
	if len(got.Parts) != 2 || got.Parts[1].ImageURL.URL != "https://example.com/cat.png" {
		t.Errorf("round trip: got %+v", got.Parts)
	}
}

func TestMessage_UnmarshalNoContentField(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","tool_calls":[]}`), &m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Content != "" || m.Parts != nil {
		t.Errorf("expected zero-value content/parts, got content=%q parts=%+v", m.Content, m.Parts)
	}
}

func TestMessage_UnmarshalInvalidContentShape(t *testing.T) {
	var m Message
	err := json.Unmarshal([]byte(`{"role":"user","content":42}`), &m)
	if err == nil {
		t.Fatal("expected error for content that is neither a string nor an array")
	}
}

// RequestEnvelope-level round trip: confirms the custom Message codec
// composes correctly inside a full request, including a text-only sibling
// message in the same array.
func TestRequestEnvelope_MixedTextAndMultimodalMessages(t *testing.T) {
	raw := `{"model":"gpt-4o","messages":[
		{"role":"system","content":"be concise"},
		{"role":"user","content":[{"type":"text","text":"what's this?"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,BBBB"}}]}
	]}`
	var req RequestEnvelope
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Content != "be concise" || req.Messages[0].Parts != nil {
		t.Errorf("system message should be unaffected: got %+v", req.Messages[0])
	}
	if len(req.Messages[1].Parts) != 2 {
		t.Errorf("user message parts: got %d, want 2", len(req.Messages[1].Parts))
	}
}

// FuzzMessage_ContentPolymorphism targets the custom UnmarshalJSON added
// for RFC Gap 10 (vision): "content" is polymorphic on the wire (bare
// string vs. content-part array), detected by peeking at the first byte
// of the raw JSON. That's exactly the kind of hand-rolled parsing that's
// easy to get subtly wrong on malformed input — huge base64 payloads,
// broken data: URIs, mixed/nested shapes, wrong types where a string or
// array is expected. The only property under fuzzing is "never panic";
// Unmarshal returning an error for genuinely malformed input is fine and
// expected.
func FuzzMessage_ContentPolymorphism(f *testing.F) {
	seeds := []string{
		`{"role":"user","content":"hello"}`,
		`{"role":"user","content":[{"type":"text","text":"hi"}]}`,
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}`,
		`{"role":"user","content":null}`,
		`{"role":"user","content":42}`,
		`{"role":"user","content":true}`,
		`{"role":"user","content":[]}`,
		`{"role":"user","content":[{}]}`,
		`{"role":"user","content":[{"type":"image_url","image_url":null}]}`,
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"not-a-real-uri"}}]}`,
		`{"role":"user","content":[null]}`,
		`{"role":"user","content":[[{"type":"text","text":"nested"}]]}`,
		`{"role":"user","content":"` + strings.Repeat("A", 200000) + `"}`,
		`{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("x", 100000) + `"}]}`,
		`{"role":"user"`,        // truncated
		`{"content":"no role"}`, // missing role
		``,
		`null`,
		`[]`,
		`"just a string"`,
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("Q", 500000) + `"}}]}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		var m Message
		if err := json.Unmarshal(b, &m); err != nil {
			return // malformed input erroring is fine; the property under test is "no panic"
		}
		// If it decoded, it must also re-encode without panicking, and the
		// result must remain decodable (round-trip stability).
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("Marshal of a successfully-Unmarshaled Message failed: %v (message: %+v)", err, m)
		}
		var roundTrip Message
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("re-Unmarshal of Marshaled output failed: %v (encoded: %s)", err, encoded)
		}
	})
}
