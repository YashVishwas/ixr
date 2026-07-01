package routing

import "strings"

// contextLengthPhrases are substrings found in provider error bodies when a
// request exceeds the model's context window. Checked against err.Error().
var contextLengthPhrases = []string{
	// OpenAI / OpenAI-compat (GPT, Cerebras, Groq, OpenRouter, etc.)
	"context_length_exceeded",
	"maximum context length",
	"max_tokens is too large",
	"reduce the length of the messages",
	// Anthropic
	"prompt is too long",
	"exceeds the maximum",
	// Google / Gemini
	"exceeds the limit",
	"request too large",
	// Mistral
	"tokens exceeds the model's maximum context",
	// Generic
	"context window",
	"too many tokens",
}

// IsContextLengthError reports whether err is a provider context-window overflow.
// It inspects the error string for known provider-specific phrases rather than
// requiring adapters to return a typed error — keeping adapters unchanged.
func IsContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, phrase := range contextLengthPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}
