package piiguardrail

import (
	"context"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeReq(content string) *schema.RequestEnvelope {
	return &schema.RequestEnvelope{
		Model:    "gpt-4o",
		Messages: []schema.Message{{Role: "user", Content: content}},
	}
}

// --- Block mode ---

func TestBlock_Email(t *testing.T) {
	p := New(ModeBlock)
	req := makeReq("Please contact john.doe@example.com for details.")
	err := p.Intercept(context.Background(), req)
	if err == nil {
		t.Fatal("expected block on email")
	}
	blocked, ok := err.(*guardrail.BlockedError)
	if !ok {
		t.Fatalf("expected BlockedError, got %T", err)
	}
	if blocked.Category != "email" {
		t.Fatalf("expected category=email, got %q", blocked.Category)
	}
}

func TestBlock_Phone(t *testing.T) {
	p := New(ModeBlock)
	err := p.Intercept(context.Background(), makeReq("Call me at 555-867-5309."))
	if err == nil {
		t.Fatal("expected block on phone")
	}
}

func TestBlock_SSN(t *testing.T) {
	p := New(ModeBlock)
	err := p.Intercept(context.Background(), makeReq("My SSN is 123-45-6789."))
	if err == nil {
		t.Fatal("expected block on SSN")
	}
	if blocked, ok := err.(*guardrail.BlockedError); ok {
		if blocked.Category != "us_ssn" {
			t.Fatalf("expected category=us_ssn, got %q", blocked.Category)
		}
	}
}

func TestBlock_CreditCard_ValidLUHN(t *testing.T) {
	p := New(ModeBlock)
	// 4111111111111111 is the canonical Visa test number — passes LUHN.
	err := p.Intercept(context.Background(), makeReq("Card number: 4111 1111 1111 1111"))
	if err == nil {
		t.Fatal("expected block on valid credit card number")
	}
}

func TestBlock_CreditCard_InvalidLUHN(t *testing.T) {
	p := New(ModeBlock)
	// 4111111111111112 fails LUHN — use a prompt with no other PII patterns.
	err := p.Intercept(context.Background(), makeReq("The product serial is PROD4111111111111112END"))
	if err != nil {
		t.Fatalf("invalid LUHN should not block, got: %v", err)
	}
}

func TestBlock_CreditCard_ISBN(t *testing.T) {
	p := New(ModeBlock)
	// 9780306406157 is a real ISBN-13 — fails LUHN, should pass through.
	err := p.Intercept(context.Background(), makeReq("See book ISBN9780306406157 for reference"))
	if err != nil {
		t.Fatalf("ISBN should not be blocked (fails LUHN): %v", err)
	}
}

func TestLUHN_KnownValues(t *testing.T) {
	cases := []struct {
		number string
		valid  bool
	}{
		{"4111111111111111", true},  // Visa test
		{"5500005555555559", true},  // Mastercard test
		{"4111111111111112", false}, // off by one
		{"1234567890123456", false}, // random digits
		{"79927398713", true},       // LUHN spec example
		{"79927398714", false},      // LUHN spec invalid
	}
	for _, tc := range cases {
		if got := luhn(tc.number); got != tc.valid {
			t.Errorf("luhn(%q) = %v, want %v", tc.number, got, tc.valid)
		}
	}
}

func TestBlock_Clean(t *testing.T) {
	p := New(ModeBlock)
	err := p.Intercept(context.Background(), makeReq("What is the capital of France?"))
	if err != nil {
		t.Fatalf("expected no block on clean prompt, got: %v", err)
	}
}

func TestBlock_AssistantRoleSkipped(t *testing.T) {
	// PII in assistant turns should not be scanned — we match on input, not history.
	p := New(ModeBlock)
	req := &schema.RequestEnvelope{
		Model: "gpt-4o",
		Messages: []schema.Message{
			{Role: "assistant", Content: "Contact support@example.com for help."},
			{Role: "user", Content: "Thanks, can you summarise that?"},
		},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("assistant-role PII should not trigger block: %v", err)
	}
}

// --- Redact mode ---

func TestRedact_Email(t *testing.T) {
	p := New(ModeRedact)
	req := makeReq("Email me at jane.smith@company.org please.")
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("redact mode should not return error: %v", err)
	}
	if strings.Contains(req.Messages[0].Content, "jane.smith@company.org") {
		t.Fatal("email should have been redacted")
	}
	if !strings.Contains(req.Messages[0].Content, "[REDACTED:EMAIL]") {
		t.Fatalf("expected [REDACTED:EMAIL] in content, got: %s", req.Messages[0].Content)
	}
}

func TestRedact_MultipleCategories(t *testing.T) {
	p := New(ModeRedact)
	req := makeReq("Email: test@example.com, SSN: 123-45-6789")
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := req.Messages[0].Content
	if strings.Contains(content, "test@example.com") {
		t.Fatal("email should be redacted")
	}
	if strings.Contains(content, "123-45-6789") {
		t.Fatal("SSN should be redacted")
	}
}

func TestRedact_Clean(t *testing.T) {
	p := New(ModeRedact)
	original := "Summarise the quarterly report."
	req := makeReq(original)
	_ = p.Intercept(context.Background(), req)
	if req.Messages[0].Content != original {
		t.Fatalf("clean prompt should not be modified: got %q", req.Messages[0].Content)
	}
}

// --- BlockedError ---

func TestBlockedError_String(t *testing.T) {
	err := &guardrail.BlockedError{
		Interceptor: "pii-guardrail",
		Category:    "email",
		Message:     "request contains email",
	}
	s := err.Error()
	if !strings.Contains(s, "email") {
		t.Fatalf("error string missing category: %s", s)
	}
}
