package ingress

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	domainfeedback "github.com/YashVishwas/ixr/internal/domain/feedback"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
)

type recordingBandit struct {
	calls []struct {
		model  string
		reward float64
	}
}

func (r *recordingBandit) Select(candidates []routing.Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].Model
}

func (r *recordingBandit) Update(model string, reward float64) {
	r.calls = append(r.calls, struct {
		model  string
		reward float64
	}{model, reward})
}

func (r *recordingBandit) Regret() *scoring.RegretTracker { return nil }

func postFeedback(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/feedback", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestFeedbackHandler_PositiveRating_UpdatesBanditWithFullReward(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	store.Record("chatcmpl-1", domainfeedback.CallInfo{Model: "gpt-4o", AutoRouted: true})
	bandit := &recordingBandit{}
	h := NewFeedbackHandler(store, bandit)

	w := postFeedback(t, h, `{"call_id":"chatcmpl-1","rating":"positive"}`)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", w.Code)
	}
	if len(bandit.calls) != 1 || bandit.calls[0].model != "gpt-4o" || bandit.calls[0].reward != 1.0 {
		t.Fatalf("expected one update(gpt-4o, 1.0), got %+v", bandit.calls)
	}
}

func TestFeedbackHandler_NegativeRating_UpdatesBanditWithZeroReward(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	store.Record("chatcmpl-1", domainfeedback.CallInfo{Model: "gpt-4o", AutoRouted: true})
	bandit := &recordingBandit{}
	h := NewFeedbackHandler(store, bandit)

	w := postFeedback(t, h, `{"call_id":"chatcmpl-1","rating":"negative"}`)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", w.Code)
	}
	if len(bandit.calls) != 1 || bandit.calls[0].reward != 0.0 {
		t.Fatalf("expected one update(_, 0.0), got %+v", bandit.calls)
	}
}

func TestFeedbackHandler_NonAutoRoutedCall_AcceptedButBanditUntouched(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	store.Record("chatcmpl-1", domainfeedback.CallInfo{Model: "gpt-4o", AutoRouted: false})
	bandit := &recordingBandit{}
	h := NewFeedbackHandler(store, bandit)

	w := postFeedback(t, h, `{"call_id":"chatcmpl-1","rating":"positive"}`)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204 (feedback on an explicit model request is still accepted)", w.Code)
	}
	if len(bandit.calls) != 0 {
		t.Errorf("expected no bandit update for a non-auto-routed call, got %+v", bandit.calls)
	}
}

func TestFeedbackHandler_UnknownCallID_404(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	bandit := &recordingBandit{}
	h := NewFeedbackHandler(store, bandit)

	w := postFeedback(t, h, `{"call_id":"does-not-exist","rating":"positive"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
	if len(bandit.calls) != 0 {
		t.Errorf("expected no bandit update for an unknown call ID, got %+v", bandit.calls)
	}
}

func TestFeedbackHandler_MissingCallID_400(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	h := NewFeedbackHandler(store, &recordingBandit{})

	w := postFeedback(t, h, `{"rating":"positive"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestFeedbackHandler_InvalidRating_400(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	store.Record("chatcmpl-1", domainfeedback.CallInfo{Model: "gpt-4o", AutoRouted: true})
	bandit := &recordingBandit{}
	h := NewFeedbackHandler(store, bandit)

	w := postFeedback(t, h, `{"call_id":"chatcmpl-1","rating":"meh"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if len(bandit.calls) != 0 {
		t.Errorf("expected no bandit update for an invalid rating, got %+v", bandit.calls)
	}
}

func TestFeedbackHandler_MalformedJSON_400(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	h := NewFeedbackHandler(store, &recordingBandit{})

	w := postFeedback(t, h, `not json`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestFeedbackHandler_WrongMethod_405(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	h := NewFeedbackHandler(store, &recordingBandit{})

	req := httptest.NewRequest(http.MethodGet, "/v1/feedback", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
}
