package anthropic

import (
	"context"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
	"github.com/YashVishwas/ixr/plugins/compressor"
)

// This file pins the invariant plugins/compressor and cache_control
// (translate.go's maybeCacheSystemBlock) both currently rely on without
// coordinating explicitly: compressor never touches the system message, and
// cache_control is only ever applied to the system block. Today that means
// there's no live conflict — but nothing stops a future change to either
// feature from breaking that unstated agreement independently. These tests
// exist so such a change fails loudly here instead of silently degrading
// Anthropic cache hit rates or silently breaking compression.
//
// If cache_control is ever extended to non-system blocks (Anthropic
// supports it on any content block), these two features would need an
// actual shared signal — e.g. a Pinned field on schema.Message — rather
// than each independently assuming the other stays out of its way.

// TestCompressorAndCacheControl_ComposeOnSameRequest is the real
// integration case: a long system prompt (cache-eligible) and a long
// tool-result message (compression-eligible) in the same request, run
// through the actual interceptor and then the actual Anthropic translator —
// not two isolated unit tests asserting the same thing in parallel.
func TestCompressorAndCacheControl_ComposeOnSameRequest(t *testing.T) {
	longSystem := strings.Repeat("be a careful, precise assistant. ", 200) // ~6800 chars / ~1700 tokens, well over the 1024-token cache threshold
	longToolResult := strings.Repeat("data ", 500)                        // well over the compressor's default threshold

	req := &schema.RequestEnvelope{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{
			{Role: "system", Content: longSystem},
			{Role: "user", Content: "old question"},
			{Role: "tool", ToolCallID: "t1", Content: longToolResult},
			{Role: "user", Content: "new question"},
		},
	}

	if err := compressor.New(0).Intercept(context.Background(), req); err != nil {
		t.Fatalf("compressor.Intercept: unexpected error: %v", err)
	}

	// The compressor must have left the system message byte-identical —
	// otherwise cache_control's premise (a stable, repeated prefix) is
	// undermined by the very feature that's supposed to compose with it.
	if req.Messages[0].Content != longSystem {
		t.Fatalf("compressor modified the system message — this breaks cache_control's byte-stability requirement")
	}
	// The tool result should actually have been compressed — confirms this
	// test is exercising the real compression path, not a no-op.
	if len(req.Messages[2].Content) >= len(longToolResult) {
		t.Fatalf("expected the tool result to be compressed, got len=%d (original len=%d)", len(req.Messages[2].Content), len(longToolResult))
	}

	got := toWireRequest(req)

	// cache_control must still be present on the (untouched) system block.
	if len(got.System) != 1 || got.System[0].CacheControl == nil {
		t.Errorf("expected cache_control on the system block after compression ran, got %+v", got.System)
	}

	// cache_control must not have leaked onto any other block — pins the
	// "only ever on the system block" half of the invariant.
	assertNoCacheControlOutsideSystem(t, got)
}

// TestCacheControl_NeverAppliedOutsideSystemBlock pins the same invariant
// independent of the compressor entirely — a rich request with tool calls,
// tool results, and an image should still never carry cache_control
// anywhere but the (long, cache-eligible) system block.
func TestCacheControl_NeverAppliedOutsideSystemBlock(t *testing.T) {
	longSystem := strings.Repeat("be a careful, precise assistant. ", 200)
	req := &schema.RequestEnvelope{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{
			{Role: "system", Content: longSystem},
			{Role: "user", Parts: []schema.ContentPart{
				{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "https://example.com/cat.png"}},
			}},
			{Role: "assistant", ToolCalls: []schema.ToolCall{
				{ID: "toolu_1", Type: "function", Function: schema.ToolFunction{Name: "get_weather", Arguments: `{"city":"Austin"}`}},
			}},
			{Role: "tool", ToolCallID: "toolu_1", Content: "72F and sunny"},
		},
	}

	got := toWireRequest(req)

	if len(got.System) != 1 || got.System[0].CacheControl == nil {
		t.Fatalf("expected cache_control on the system block, got %+v", got.System)
	}
	assertNoCacheControlOutsideSystem(t, got)
}

// assertNoCacheControlOutsideSystem walks every content block in a
// translated wireRequest except the system block and fails if any of them
// carry cache_control — today nothing sets it there, but nothing enforces
// that either without this check.
func assertNoCacheControlOutsideSystem(t *testing.T, wr wireRequest) {
	t.Helper()
	for i, m := range wr.Messages {
		for j, c := range m.Content {
			if c.CacheControl != nil {
				t.Errorf("message %d block %d (role=%s, type=%s) unexpectedly carries cache_control", i, j, m.Role, c.Type)
			}
		}
	}
}
