package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/session"
	"github.com/YashVishwas/ixr/pkg/schema"
)

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
// Streaming requests (req.Stream=true) receive history injection but the
// response is not captured — buffering an SSE stream would defeat its purpose.
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

	// 5. Load stored history and prepend to the incoming messages.
	history, _ := m.store.Get(r.Context(), storeKey)
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

	// 7. Echo the session ID in the response so the client can persist it.
	w.Header().Set(headerSessionID, sessionID)

	// 8. Streaming: history is injected above but we can't capture the SSE response.
	if req.Stream {
		m.next.ServeHTTP(w, r)
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
