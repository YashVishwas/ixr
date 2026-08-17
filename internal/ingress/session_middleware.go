package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/session"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// contextHeadroom is the fraction of the context window reserved for the
// system prompt, new user message, and the model's response.
// History is trimmed to fit within the remaining 80%.
const contextHeadroom = 0.80

const (
	headerSessionID    = "X-IXR-Session-ID"
	headerSessionReset = "X-IXR-Session-Reset"
)

// SessionMiddleware injects stored conversation history into each request and
// appends the new user+assistant turn to the session store after the response.
//
// Clients supply X-IXR-Session-ID to identify their session. If absent, the
// server generates a UUID and returns it in the response header so the client
// can use it on subsequent requests.
//
// Streaming requests (req.Stream=true) receive history injection and the
// assistant's full reply is still captured — see sseCaptureWriter and
// parseSSEAssistantTurn (session_stream_capture.go) — by teeing bytes to
// the client in real time rather than buffering the stream itself, so
// capture adds no latency to what the client sees.
type SessionMiddleware struct {
	store session.SessionStore
	next  http.Handler
}

// NewSessionMiddleware creates a session middleware wrapping next.
func NewSessionMiddleware(store session.SessionStore, next http.Handler) *SessionMiddleware {
	return &SessionMiddleware{store: store, next: next}
}

func (m *SessionMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Resolve session ID — client-supplied or server-generated.
	sessionID := r.Header.Get(headerSessionID)
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// 2. Build the tenant-scoped store key to prevent cross-tenant access.
	id := identity.FromContext(r.Context())
	storeKey := id.TenantID + ":" + sessionID

	// 3. Handle explicit reset before loading history.
	if r.Header.Get(headerSessionReset) == "true" {
		m.store.Delete(r.Context(), storeKey)
	}

	// 4. Decode the request body.
	var req schema.RequestEnvelope
	body := &bodyCapture{ReadCloser: r.Body}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		// Malformed body — pass through; chat handler will return 400.
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}

	// 5. Load stored history, trim to fit the model's context window, then prepend.
	history, _ := m.store.Get(r.Context(), storeKey)
	history = trimToContextWindow(history, req.Messages, req.Model)
	historyLen := len(history)
	if historyLen > 0 {
		req.Messages = append(history, req.Messages...)
	}

	// 6. Re-encode the modified request as the new body.
	encoded, err := json.Marshal(req)
	if err != nil {
		slog.Debug("session: failed to re-encode request", "err", err)
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))

	// 6b. Tell the cache layer how much of the message list is injected
	// history, not the caller's new turn — see cache.WithHistoryLen.
	r = r.WithContext(cache.WithHistoryLen(r.Context(), historyLen))

	// 7. Echo the session ID in the response so the client can persist it.
	w.Header().Set(headerSessionID, sessionID)

	// 8. Streaming: history is injected above; the response is captured via
	// sseCaptureWriter, which tees bytes to the real client in real time
	// (zero added latency — see its doc comment) while also buffering them
	// for a post-stream parse pass, so the new user+assistant turn still
	// gets persisted the same as the non-streaming path below.
	if req.Stream {
		capture := &sseCaptureWriter{ResponseWriter: w}
		m.next.ServeHTTP(capture, r)

		if assistantTurn, ok := parseSSEAssistantTurn(capture.buf.Bytes()); ok {
			userTurn := lastUserMessage(req.Messages, historyLen)
			m.store.Append(r.Context(), storeKey, userTurn, assistantTurn)
		}
		return
	}

	// 9. Capture the response body to extract the assistant turn.
	rec := &responseRecorder{ResponseWriter: w, headerCode: http.StatusOK}
	m.next.ServeHTTP(rec, r)

	// 10. On success, persist the new user+assistant turn to the session store.
	if rec.headerCode == http.StatusOK && len(rec.body) > 0 {
		var resp schema.ResponseEnvelope
		if err := json.Unmarshal(rec.body, &resp); err == nil && len(resp.Choices) > 0 {
			userTurn := lastUserMessage(req.Messages, historyLen)
			assistantTurn := resp.Choices[0].Message
			if assistantTurn.Role == "" {
				assistantTurn.Role = "assistant"
			}
			m.store.Append(r.Context(), storeKey, userTurn, assistantTurn)
		}
	}
}

// lastUserMessage finds the last user-role message among the new messages sent
// in this request (i.e., at index >= historyLen in the combined slice).
func lastUserMessage(messages []schema.Message, historyLen int) schema.Message {
	for i := len(messages) - 1; i >= historyLen; i-- {
		if messages[i].Role == "user" {
			return messages[i]
		}
	}
	return schema.Message{Role: "user"}
}

// trimToContextWindow drops the oldest history pairs (user+assistant) until the
// combined token estimate of history + incoming messages fits within the model's
// context window budget. Returns the trimmed history slice.
func trimToContextWindow(history []schema.Message, incoming []schema.Message, model string) []schema.Message {
	if len(history) == 0 {
		return history
	}
	window := routing.ContextWindowFor(model)
	budget := int(float64(window) * contextHeadroom)

	// Count tokens already consumed by the incoming messages.
	incomingTokens := estimateTokens(incoming)
	available := budget - incomingTokens
	if available <= 0 {
		// Incoming messages alone fill the budget — drop all history.
		slog.Debug("session: context window full, dropping all history",
			"model", model, "window", window, "incoming_tokens", incomingTokens)
		return nil
	}

	// Walk history from the end (most recent) and keep turns that fit.
	// History is stored as pairs: [user, assistant, user, assistant, ...].
	// We drop from the front (oldest) so the most recent context is preserved.
	for estimateTokens(history) > available {
		if len(history) < 2 {
			return nil
		}
		// Drop the oldest pair.
		dropped := history[:2]
		history = history[2:]
		slog.Debug("session: trimmed oldest turn to fit context window",
			"model", model, "dropped_tokens", estimateTokens(dropped))
	}
	return history
}

// estimateTokens approximates the token count for a slice of messages.
// Uses the standard rule of thumb: 1 token ≈ 4 characters.
// Adds 4 tokens per message for role/formatting overhead.
func estimateTokens(messages []schema.Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content)/4 + 4
	}
	return total
}
