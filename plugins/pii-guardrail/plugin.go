// Package piiguardrail provides a RequestInterceptor that scans outbound
// prompts for Personally Identifiable Information before they are sent to
// an upstream LLM provider.
//
// Two modes:
//   - block: return 403 when PII is detected; the request is not forwarded.
//   - redact: replace matched PII with [REDACTED:TYPE] and allow the request.
//
// All scanning runs in-process with no external calls, so no prompt content
// leaves the operator's infrastructure during the scan itself.
package piiguardrail

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/YashVishwas/ixr/internal/domain/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Mode controls what happens when PII is detected.
type Mode string

const (
	ModeBlock  Mode = "block"  // reject the request with a 403
	ModeRedact Mode = "redact" // replace PII in-place and allow the request
)

// category holds a PII pattern and its human-readable name.
type category struct {
	name    string
	pattern *regexp.Regexp
}

// builtinCategories covers the most common PII types.
// Patterns are intentionally conservative — false negatives are safer than
// false positives that block legitimate requests.
var builtinCategories = []category{
	{
		name: "email",
		// RFC 5322-ish: local@domain.tld
		pattern: regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`),
	},
	{
		name: "us_phone",
		// US phone: (555) 867-5309, 555-867-5309, +15558675309
		pattern: regexp.MustCompile(`(?:\+1[\s\-]?)?(?:\(?\d{3}\)?[\s\-]?)?\d{3}[\s\-]\d{4}`),
	},
	{
		name: "us_ssn",
		// XXX-XX-XXXX or XXXXXXXXX (9 digits)
		pattern: regexp.MustCompile(`\b\d{3}[-\s]?\d{2}[-\s]?\d{4}\b`),
	},
	{
		name: "credit_card",
		// 13-19 digit sequences with optional spaces/dashes; LUHN-validated before blocking.
		pattern: regexp.MustCompile(`\b(?:\d[ \-]?){13,19}\b`),
	},
	{
		name: "ip_address",
		// IPv4
		pattern: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	},
}

// Plugin scans request messages for PII and either blocks or redacts.
type Plugin struct {
	mode       Mode
	categories []category
}

// New creates a PIIGuardrail plugin.
// mode: "block" rejects detected requests; "redact" replaces PII in-place.
// Pass nil categories to use the built-in set.
func New(mode Mode) *Plugin {
	return &Plugin{
		mode:       mode,
		categories: builtinCategories,
	}
}

func (p *Plugin) Name() string { return "pii-guardrail" }

// Intercept scans all user and system messages in req for PII.
// In block mode it returns a BlockedError on the first match.
// In redact mode it modifies req.Messages in-place and returns nil.
func (p *Plugin) Intercept(_ context.Context, req *schema.RequestEnvelope) error {
	for i, msg := range req.Messages {
		if msg.Role != "user" && msg.Role != "system" {
			continue
		}
		for _, cat := range p.categories {
			if !p.matches(cat, msg.Content) {
				continue
			}
			switch p.mode {
			case ModeBlock:
				return &guardrail.BlockedError{
					Interceptor: p.Name(),
					Category:    cat.name,
					Message:     fmt.Sprintf("request contains %s — blocked by PII guardrail", cat.name),
				}
			case ModeRedact:
				placeholder := "[REDACTED:" + strings.ToUpper(cat.name) + "]"
				req.Messages[i].Content = p.redact(cat, msg.Content, placeholder)
				msg = req.Messages[i]
			}
		}
	}
	return nil
}

// matches reports whether content contains PII in the given category.
// For credit cards, each regex match is additionally validated with LUHN
// to eliminate false positives on ISBNs, serial numbers, and product codes.
func (p *Plugin) matches(cat category, content string) bool {
	if cat.name != "credit_card" {
		return cat.pattern.MatchString(content)
	}
	for _, m := range cat.pattern.FindAllString(content, -1) {
		if luhn(m) {
			return true
		}
	}
	return false
}

// redact replaces all PII matches in content with placeholder.
// Credit card matches are LUHN-validated before replacement.
func (p *Plugin) redact(cat category, content, placeholder string) string {
	if cat.name != "credit_card" {
		return cat.pattern.ReplaceAllString(content, placeholder)
	}
	return cat.pattern.ReplaceAllStringFunc(content, func(m string) string {
		if luhn(m) {
			return placeholder
		}
		return m
	})
}

// luhn validates a numeric string using the Luhn algorithm.
// Spaces and dashes are stripped before validation.
func luhn(s string) bool {
	s = strings.NewReplacer(" ", "", "-", "").Replace(s)
	if len(s) < 10 {
		return false
	}
	var sum int
	// Walk right to left; every second digit from the right (starting at position 2)
	// is doubled. Position 1 (rightmost) is never doubled.
	for i, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
		digit := int(ch - '0')
		// Position from the right: (len-1-i). Double when that position is even (2, 4, 6...).
		posFromRight := len(s) - 1 - i
		if posFromRight%2 == 1 {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
	}
	return sum%10 == 0
}
