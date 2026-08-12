package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- bytesReader / readCloser: the io.Reader contract bug this session found ---

func TestBytesReader_ExhaustedReturnsEOF(t *testing.T) {
	br := &bytesReader{data: []byte("hi")}
	buf := make([]byte, 10)

	n, err := br.Read(buf)
	if n != 2 || err != nil {
		t.Fatalf("first read: got (%d, %v), want (2, nil)", n, err)
	}

	// The bug: this used to return (0, nil) forever instead of (0, io.EOF)
	// once exhausted — a violation of io.Reader's contract that
	// json.Decoder's underlying buffered reader takes literally, spinning
	// forever waiting for either more data or a terminal error that never
	// came.
	n, err = br.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("exhausted read: got (%d, %v), want (0, io.EOF)", n, err)
	}

	// And it must keep returning EOF, not flip back to (0, nil), if
	// something calls Read again after already observing EOF.
	n, err = br.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("read after EOF: got (%d, %v), want (0, io.EOF)", n, err)
	}
}

func TestReadCloser_DecodesACompleteRoundTrip(t *testing.T) {
	// Guards against the EOF fix breaking the normal, already-working
	// case: a complete, well-formed body must still decode cleanly.
	rc := readCloser([]byte(`{"model":"gpt-4o"}`))
	var v map[string]any
	if err := json.NewDecoder(rc).Decode(&v); err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if v["model"] != "gpt-4o" {
		t.Errorf("got %+v", v)
	}
}

func TestReadCloser_TruncatedBody_DecodeErrorsInsteadOfHanging(t *testing.T) {
	// The exact shape body.replay() produces after http.MaxBytesReader (or
	// any other partial read) cuts a body off mid-JSON-value: incomplete,
	// not just malformed. json.Decoder must be able to observe EOF and
	// return an error, not block waiting for bytes that will never come.
	rc := readCloser([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"truncated here...`))

	done := make(chan error, 1)
	go func() {
		var v map[string]any
		done <- json.NewDecoder(rc).Decode(&v)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a decode error for a truncated body, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Decode on a truncated replayed body did not return within 2s — regression of the exhausted-reader EOF bug")
	}
}

// --- CacheMiddleware end to end: the actual bug this session found ---

// TestCacheMiddleware_OversizedBody_FailsCleanlyInsteadOfHanging reproduces
// the real incident: a request body cut off by http.MaxBytesReader flows
// into CacheMiddleware, which fails to decode it, replays the captured
// partial bytes, and passes through to next — which also fails to decode
// the same truncated bytes. Before the io.EOF fix, that second decode
// spun forever instead of erroring, so the client never got a response at
// all (observed directly: curl timed out with 0 bytes received).
func TestCacheMiddleware_OversizedBody_FailsCleanlyInsteadOfHanging(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_body", "could not parse request JSON")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	cm := NewCacheMiddleware(nil, time.Minute, next)

	oversizedJSON := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("a", 1000) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(oversizedJSON))
	req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 200)

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		cm.ServeHTTP(w, req)
		done <- w.Code
	}()

	select {
	case code := <-done:
		if code != http.StatusBadRequest {
			t.Errorf("status: got %d, want 400", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not complete within 2s — the CacheMiddleware -> next replay chain hung on an oversized body")
	}
}
