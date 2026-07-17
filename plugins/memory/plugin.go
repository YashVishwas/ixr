// Package memory provides a user memory plugin for ixr.
// It extracts memorable facts from conversation turns (post-call, async)
// and injects them into new requests as context (pre-call, via middleware).
package memory

import (
	"context"
	"strings"

	"github.com/YashVishwas/ixr/internal/domain/memory"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Plugin is an EventConsumer that extracts facts from CallEvents and stores
// them in the user memory store. It runs asynchronously post-response so
// extraction never adds latency to the request path.
type Plugin struct {
	store     memory.Store
	extractor memory.Extractor
}

// New creates a memory plugin.
func New(store memory.Store, extractor memory.Extractor) *Plugin {
	return &Plugin{store: store, extractor: extractor}
}

// Name returns the stable plugin identifier.
func (p *Plugin) Name() string { return "user-memory" }

// OnEvent extracts memorable facts from the call and stores them.
// Only processes non-streaming, non-shadow, non-error calls with a UserID.
func (p *Plugin) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
	if ev.Error != "" || ev.Streaming || ev.Shadow != nil {
		return nil
	}

	userKey := userKeyFromEvent(ev)
	if userKey == "" {
		return nil // no UserID — can't associate memory
	}

	turn := memory.ConversationTurn{
		UserMessage:      lastUserMessage(ev.Request.Messages),
		AssistantMessage: firstAssistantContent(ev.Response),
	}
	if turn.UserMessage == "" {
		return nil
	}

	entries, err := p.extractor.Extract(ctx, userKey, turn)
	if err != nil || len(entries) == 0 {
		return err
	}

	for _, e := range entries {
		_ = p.store.Save(ctx, e)
	}
	return nil
}

// userKeyFromEvent builds a stable key from event fields.
// Returns "" when UserID is not available (memories are user-scoped).
func userKeyFromEvent(ev *schema.CallEvent) string {
	// CallEvent doesn't carry UserID directly — it's in Identity which is
	// set in request context but not serialised onto CallEvent yet.
	// We use TenantID alone when no finer grain is available.
	// When UserID is added to CallEvent in a future schema version,
	// update this to: ev.TenantID + ":" + ev.UserID
	if ev.TenantID == "" || ev.TenantID == "default" {
		return ""
	}
	return ev.TenantID
}

func lastUserMessage(msgs []schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func firstAssistantContent(resp schema.ResponseEnvelope) string {
	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}
	return ""
}

// FormatMemories formats a slice of entries as a compact system-message string
// for injection into a new session's context.
func FormatMemories(entries []memory.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("What you know about this user (from prior conversations):\n")
	for _, e := range entries {
		b.WriteString("- ")
		b.WriteString(e.Content)
		b.WriteByte('\n')
	}
	return b.String()
}
