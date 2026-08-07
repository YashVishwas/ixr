// Package compressor implements a guardrail.RequestInterceptor that shrinks
// long tool-result and history content before a request reaches routing,
// caching, or the provider — the same goal RAG-chunk/context compression
// tools pursue, scoped to fit ixr's existing interceptor extension point
// rather than adding a new one.
//
// guardrail.RequestInterceptor already receives req as a pointer, and
// InterceptorMiddleware already re-marshals req back into the request body
// after Intercept returns (see internal/ingress/interceptor_middleware.go) —
// an interceptor that mutates req in place and returns nil (never blocking)
// is already a full pre-routing request transformer with no new interface
// or chain needed.
//
// Still deliberately narrow, not a semantic compressor: valid JSON gets a
// structure-aware pass (see json.go), everything else gets line-oriented
// collapsing, and anything still oversized after that gets truncated with a
// marker. It never touches the system prompt or the caller's own latest
// turn — only tool-result messages and older history are eligible.
package compressor

import (
	"context"
	"fmt"
	"strings"

	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var _ guardrail.RequestInterceptor = (*Plugin)(nil)

const defaultMaxChars = 4000

// Plugin compresses eligible message content before the request is routed,
// cached, or sent to a provider. It never blocks — Intercept always returns
// nil — so it's safe to chain alongside interceptors that do (e.g.
// plugins/budget).
type Plugin struct {
	maxChars int
}

// New creates a compressor. maxChars <= 0 uses defaultMaxChars (4000,
// ~1000 tokens at the standard 4-chars-per-token estimate).
func New(maxChars int) *Plugin {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	return &Plugin{maxChars: maxChars}
}

// Name returns the stable plugin identifier.
func (p *Plugin) Name() string { return "request-compressor" }

// Intercept compresses eligible messages in place. It never returns an
// error — compression is a size optimization, not a policy gate.
func (p *Plugin) Intercept(_ context.Context, req *schema.RequestEnvelope) error {
	liveTurn := lastUserMessageIndex(req.Messages)
	for i := range req.Messages {
		if i == liveTurn {
			continue // never touch the caller's own live turn
		}
		m := &req.Messages[i]
		if !eligible(m) {
			continue
		}
		m.Content = compress(m.Content, p.maxChars)
	}
	return nil
}

// eligible reports whether a message is a compression candidate at all —
// tool-result output (Role == "tool") or any other non-system,
// non-multimodal turn with text content. It does not gate on length:
// compress is already a no-op (beyond harmless whitespace/duplicate-line
// collapsing) on content short enough that no threshold is crossed, so
// there's no separate size check to duplicate here. System prompts are
// never touched — see Anthropic's cache_control (a different, complementary
// optimization for the same content) for why the system prompt already gets
// special handling elsewhere rather than mutation here.
func eligible(m *schema.Message) bool {
	if len(m.Parts) > 0 || m.Role == "system" {
		return false
	}
	return m.Content != ""
}

// lastUserMessageIndex finds the caller's actual new turn — the last
// role=="user" message — so it's never compressed. Returns -1 if there is
// no user message (e.g. an empty or malformed request), in which case no
// index will match the loop above and every eligible message is considered.
func lastUserMessageIndex(messages []schema.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return -1
}

// compress applies content-aware compression, then truncates with a marker
// if the result still exceeds maxChars. Valid JSON goes through
// compressJSON (structure-aware — see json.go), which already does its own
// deduplication (schema-form collapses repeated keys, the only thing
// json.Marshal's compact fallback could ever have in common with the line
// heuristic is that neither has anything left to collapse). Everything
// else falls back to line-oriented collapsing. Below maxChars after that,
// it's a no-op beyond whatever collapsing already did.
func compress(content string, maxChars int) string {
	collapsed, ok := compressJSON(content)
	if !ok {
		collapsed = collapseRepeatedLines(collapseBlankLines(content))
	}
	if len(collapsed) <= maxChars {
		return collapsed
	}
	return collapsed[:maxChars] + fmt.Sprintf("\n... [truncated %d chars]", len(collapsed)-maxChars)
}

// collapseBlankLines trims trailing whitespace from each line and collapses
// 2+ consecutive blank lines into one — a common shape in tool output
// (formatted JSON, log dumps) that adds bytes without adding information.
func collapseBlankLines(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	blankRun := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" {
			if blankRun {
				continue
			}
			blankRun = true
		} else {
			blankRun = false
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}

// collapseRepeatedLines replaces a run of 3+ identical consecutive
// non-blank lines with one copy plus a count marker — the shape of e.g. a
// log dump with a repeated error line, or a JSON array of near-identical
// records logged verbatim.
func collapseRepeatedLines(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		line := lines[i]
		j := i + 1
		for j < len(lines) && lines[j] == line {
			j++
		}
		runLen := j - i
		if line != "" && runLen >= 3 {
			out = append(out, line, fmt.Sprintf("... (repeated %d times)", runLen))
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return strings.Join(out, "\n")
}
