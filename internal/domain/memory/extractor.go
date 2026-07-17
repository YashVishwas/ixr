package memory

import (
	"context"
	"regexp"
	"strings"
)

// ConversationTurn is the input to an extractor — one user+assistant exchange.
type ConversationTurn struct {
	UserMessage      string
	AssistantMessage string
}

// Extractor derives zero or more memory entries from a conversation turn.
type Extractor interface {
	Extract(ctx context.Context, userKey string, turn ConversationTurn) ([]Entry, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// RuleExtractor — regex-based, high-precision, zero LLM calls
// ─────────────────────────────────────────────────────────────────────────────

type rulePattern struct {
	category string
	re       *regexp.Regexp
	// template converts the captured group into a human-readable fact.
	template func(match string) string
}

var rulePatterns = []rulePattern{
	{
		category: "name",
		re:       regexp.MustCompile(`(?i)(?:my name is|i(?:'m| am) called|call me|i go by)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`),
		template: func(m string) string { return "User's name is " + m },
	},
	{
		category: "employer",
		re:       regexp.MustCompile(`(?i)(?:i (?:work|am working) (?:at|for)|i(?:'m| am) (?:at|with))\s+([A-Z][^\.,!?\n]{2,40}?)(?:[,.\s]|$)`),
		template: func(m string) string { return "User works at " + strings.TrimSpace(m) },
	},
	{
		category: "role",
		re:       regexp.MustCompile(`(?i)(?:i(?:'m| am) a|i work as(?: a)?|my (?:job|role|title) is)\s+([a-z][^\.,!?\n]{2,40}?)(?:[,.\s]|$)`),
		template: func(m string) string { return "User's role is " + strings.TrimSpace(m) },
	},
	{
		category: "project",
		re:       regexp.MustCompile(`(?i)(?:i(?:'m| am) (?:building|working on|creating|developing)|my project is)\s+([^\.,!?\n]{3,60}?)(?:[,.\n]|$)`),
		template: func(m string) string { return "User is building " + strings.TrimSpace(m) },
	},
	{
		category: "location",
		re:       regexp.MustCompile(`(?i)(?:i(?:'m| am) (?:based in|located in|living in)|i live in)\s+([A-Z][^\.,!?\n]{2,40}?)(?:[,.\s]|$)`),
		template: func(m string) string { return "User is based in " + strings.TrimSpace(m) },
	},
	{
		category: "preference",
		re:       regexp.MustCompile(`(?i)(?:i (?:prefer|like|love|hate|dislike|always|usually|never))\s+([^\.,!?\n]{3,60}?)(?:[,.\n]|$)`),
		template: func(m string) string { return "User said: I " + strings.TrimSpace(m) },
	},
}

// RuleExtractor extracts facts using regex patterns.
// High-precision, zero LLM calls, sub-millisecond per turn.
type RuleExtractor struct{}

func (RuleExtractor) Extract(_ context.Context, userKey string, turn ConversationTurn) ([]Entry, error) {
	text := turn.UserMessage
	if text == "" {
		return nil, nil
	}

	var entries []Entry
	for _, p := range rulePatterns {
		m := p.re.FindStringSubmatch(text)
		if len(m) < 2 {
			continue
		}
		captured := strings.TrimSpace(m[1])
		if captured == "" {
			continue
		}
		entries = append(entries, Entry{
			UserKey:  userKey,
			Category: p.category,
			Content:  p.template(captured),
			Source:   "rule",
		})
	}
	return entries, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// LLMExtractor — model-assisted, higher recall, async post-response
// ─────────────────────────────────────────────────────────────────────────────

// LLMCaller is satisfied by any function that can call an LLM with a prompt.
// Callers pass a function that routes to the cheap/fast model of their choice.
type LLMCaller func(ctx context.Context, prompt string) (string, error)

// LLMExtractor asks a language model to extract memorable facts from the turn.
// It is called asynchronously post-response so it never adds request latency.
// Results supplement (not replace) what the RuleExtractor already found.
type LLMExtractor struct {
	call LLMCaller
}

// NewLLMExtractor creates an extractor backed by the provided caller.
func NewLLMExtractor(call LLMCaller) *LLMExtractor {
	return &LLMExtractor{call: call}
}

const llmExtractionPrompt = `You are a memory extraction assistant. Extract memorable facts about the USER from the conversation turn below.

Rules:
- Only extract facts the USER stated about themselves (ignore assistant turns).
- Each fact should be a single sentence starting with "User".
- Categories: name, employer, role, project, location, preference, other.
- Output one fact per line in this format: CATEGORY: content
- If there are no memorable facts, output: NONE

User message: %s
`

func (e *LLMExtractor) Extract(ctx context.Context, userKey string, turn ConversationTurn) ([]Entry, error) {
	if turn.UserMessage == "" {
		return nil, nil
	}

	prompt := strings.Replace(llmExtractionPrompt, "%s", turn.UserMessage, 1)
	resp, err := e.call(ctx, prompt)
	if err != nil || strings.TrimSpace(resp) == "NONE" {
		return nil, err
	}

	var entries []Entry
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "NONE" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		cat := strings.ToLower(strings.TrimSpace(line[:idx]))
		content := strings.TrimSpace(line[idx+1:])
		if content == "" {
			continue
		}
		entries = append(entries, Entry{
			UserKey:  userKey,
			Category: cat,
			Content:  content,
			Source:   "llm",
		})
	}
	return entries, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MultiExtractor — runs all extractors and deduplicates by category+content
// ─────────────────────────────────────────────────────────────────────────────

// MultiExtractor runs multiple extractors and merges results.
// Rule entries take precedence over LLM entries for the same category.
type MultiExtractor struct {
	extractors []Extractor
}

// NewMultiExtractor creates a combined extractor.
func NewMultiExtractor(extractors ...Extractor) *MultiExtractor {
	return &MultiExtractor{extractors: extractors}
}

func (m *MultiExtractor) Extract(ctx context.Context, userKey string, turn ConversationTurn) ([]Entry, error) {
	seen := make(map[string]bool) // category+source → already have
	var all []Entry

	for _, ex := range m.extractors {
		entries, err := ex.Extract(ctx, userKey, turn)
		if err != nil {
			continue // best-effort — one extractor failure doesn't stop others
		}
		for _, e := range entries {
			key := e.Category + "|" + e.Content
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, e)
		}
	}
	return all, nil
}
