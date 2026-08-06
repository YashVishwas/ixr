// Package brevity implements a guardrail.RequestInterceptor that steers
// model output toward terse, low-filler responses — the output-token
// counterpart to plugins/compressor's input-token shrinking. Unlike
// compressor, this isn't an algorithmic transform: it's a fixed instruction
// appended to the system message telling the model to drop narrative filler
// while preserving code, commands, error text, and paths exactly. Whether it
// actually reduces output tokens is a property of model behavior, not
// something a unit test can prove — see the package's test file for what is
// and isn't covered here.
package brevity

import (
	"context"
	"strings"

	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var _ guardrail.RequestInterceptor = (*Plugin)(nil)

// defaultInstruction is deliberately short — the instruction itself is
// input-token overhead paid on every request, so it should cost noticeably
// less than whatever it saves on output.
const defaultInstruction = "Be terse: drop filler and narrative explanation, prefer fragments over full sentences. Preserve code blocks, commands, error messages, paths, and URLs exactly — never compress or paraphrase those."

// Plugin appends a terseness instruction to the system message on every
// request. It never blocks — Intercept always returns nil.
type Plugin struct {
	instruction string
}

// New creates a brevity interceptor. An empty instruction uses
// defaultInstruction.
func New(instruction string) *Plugin {
	if instruction == "" {
		instruction = defaultInstruction
	}
	return &Plugin{instruction: instruction}
}

// Name returns the stable plugin identifier.
func (p *Plugin) Name() string { return "brevity" }

// Intercept appends the instruction to the existing system message, or
// creates one if the request has none. It never returns an error.
func (p *Plugin) Intercept(_ context.Context, req *schema.RequestEnvelope) error {
	idx := systemMessageIndex(req.Messages)
	if idx < 0 {
		req.Messages = append([]schema.Message{{Role: "system", Content: p.instruction}}, req.Messages...)
		return nil
	}
	req.Messages[idx].Content = appendInstruction(req.Messages[idx].Content, p.instruction)
	return nil
}

// systemMessageIndex returns the index of the first role=="system" message,
// or -1 if there is none. Only the first matters here: as with
// plugins/compressor, this package assumes callers/upstream middleware keep
// requests to a single system message — see the Anthropic translator's
// system-message concatenation fix (same change series) for what happens
// when that assumption doesn't hold.
func systemMessageIndex(messages []schema.Message) int {
	for i, m := range messages {
		if m.Role == "system" {
			return i
		}
	}
	return -1
}

func appendInstruction(existing, instruction string) string {
	existing = strings.TrimRight(existing, "\n")
	if existing == "" {
		return instruction
	}
	return existing + "\n\n" + instruction
}
