package ingress

import (
	"encoding/json"
	"net/http"

	domainfeedback "github.com/YashVishwas/ixr/internal/domain/feedback"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
)

// feedbackRequest is the POST /v1/feedback body. CallID is the id field
// from the chat completions response the caller is rating — the same
// value schema.ResponseEnvelope.ID/CallEvent.ID already carry, so a caller
// doesn't need anything ixr doesn't already hand them.
type feedbackRequest struct {
	CallID string `json:"call_id"`
	Rating string `json:"rating"` // "positive" or "negative"
}

// FeedbackHandler handles POST /v1/feedback — a caller's after-the-fact
// rating of a response, closing the loop RFC Gap 12's auto-routing bandit
// is otherwise missing: FinishReason-derived quality (plugins/
// banditreward) is free but can't distinguish a merely complete answer
// from a genuinely good one. A real human rating can.
type FeedbackHandler struct {
	store  *domainfeedback.Store
	bandit scoring.Bandit
}

// NewFeedbackHandler creates a feedback handler backed by store (populated
// by plugins/feedback from the event bus) and bandit — the same instance
// wired to scoring.Engine.SetBandit, so a rating trains the exact arm
// statistics auto-routing reads from.
func NewFeedbackHandler(store *domainfeedback.Store, bandit scoring.Bandit) *FeedbackHandler {
	return &FeedbackHandler{store: store, bandit: bandit}
}

// Reward values a rating maps to — the same [0, 1] scale
// plugins/banditreward already uses for success/latency, so a rating's
// Update call is directly comparable to (and averages naturally with) the
// automatic ones already training that arm.
const (
	positiveReward = 1.0
	negativeReward = 0.0
)

func (h *FeedbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	var req feedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "could not parse request JSON")
		return
	}
	if req.CallID == "" {
		writeError(w, http.StatusBadRequest, "missing_call_id", "call_id field is required")
		return
	}

	var reward float64
	switch req.Rating {
	case "positive":
		reward = positiveReward
	case "negative":
		reward = negativeReward
	default:
		writeError(w, http.StatusBadRequest, "invalid_rating", `rating must be "positive" or "negative"`)
		return
	}

	info, ok := h.store.Lookup(req.CallID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_call_id", "no matching call found (it may have expired, or never existed)")
		return
	}

	// A caller who named a model explicitly didn't opt into exploration —
	// the same rule plugins/banditreward already applies to the automatic
	// signal. The feedback is still accepted (200, not an error: the
	// caller did nothing wrong), it just doesn't move any arm's statistics.
	if info.AutoRouted {
		h.bandit.Update(info.Model, reward)
	}

	w.WriteHeader(http.StatusNoContent)
}
