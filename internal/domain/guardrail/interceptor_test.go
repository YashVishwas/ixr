package guardrail

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

type allowInterceptor struct{}

func (a *allowInterceptor) Name() string { return "allow" }
func (a *allowInterceptor) Intercept(_ context.Context, _ *schema.RequestEnvelope) error {
	return nil
}

type blockInterceptor struct{ msg string }

func (b *blockInterceptor) Name() string { return "block" }
func (b *blockInterceptor) Intercept(_ context.Context, _ *schema.RequestEnvelope) error {
	return &BlockedError{Interceptor: "block", Category: "test", Message: b.msg}
}

func TestChain_AllAllow(t *testing.T) {
	chain := Chain{&allowInterceptor{}, &allowInterceptor{}}
	err := chain.Intercept(context.Background(), &schema.RequestEnvelope{})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestChain_BlockShortCircuits(t *testing.T) {
	called := 0
	counter := &countingInterceptor{&called}
	chain := Chain{&blockInterceptor{"stop"}, counter}
	err := chain.Intercept(context.Background(), &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected error")
	}
	if called != 0 {
		t.Fatal("second interceptor should not run after block")
	}
}

func TestChain_Empty(t *testing.T) {
	chain := Chain{}
	if err := chain.Intercept(context.Background(), &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("empty chain should always pass: %v", err)
	}
}

type countingInterceptor struct{ n *int }

func (c *countingInterceptor) Name() string { return "counter" }
func (c *countingInterceptor) Intercept(_ context.Context, _ *schema.RequestEnvelope) error {
	*c.n++
	return nil
}

func TestWriteBlockedResponse_BlockedError(t *testing.T) {
	w := httptest.NewRecorder()
	err := &BlockedError{Interceptor: "pii", Category: "email", Message: "contains email"}
	WriteBlockedResponse(w, err)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "request_blocked") {
		t.Fatalf("expected request_blocked in body: %s", body)
	}
}

func TestWriteBlockedResponse_GenericError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteBlockedResponse(w, errors.New("generic block"))
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestBlockedError_NoCategoryInMessage(t *testing.T) {
	err := &BlockedError{Interceptor: "test", Message: "something bad"}
	if !strings.Contains(err.Error(), "something bad") {
		t.Fatalf("unexpected error string: %s", err.Error())
	}
}
