package ingress

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdmissionMiddleware_UnderCapacity_PassesThrough(t *testing.T) {
	m := NewAdmissionMiddleware(5)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := m.Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
}

func TestAdmissionMiddleware_Disabled_NeverRejects(t *testing.T) {
	m := NewAdmissionMiddleware(0) // <=0 disables admission control entirely

	// Fire more concurrent requests than any reasonable capacity would
	// allow, holding them open simultaneously — with admission control
	// disabled, every single one must still succeed.
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]int, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			blockingNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-release
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			m.Handler(blockingNext).ServeHTTP(w, req)
			results[i] = w.Code
		}(i)
	}
	time.Sleep(20 * time.Millisecond) // let all 50 goroutines reach the blocking handler
	close(release)
	wg.Wait()

	for i, code := range results {
		if code != http.StatusOK {
			t.Errorf("request %d: got status %d, want 200 (admission control disabled)", i, code)
		}
	}
}

// TestAdmissionMiddleware_AtCapacity_RejectsExcessWithServiceUnavailable is
// the core regression test: with capacity 2 and 5 concurrent requests held
// open simultaneously, exactly 2 must be admitted and 3 must be rejected
// with 503 system_overloaded — not queued, not silently dropped.
func TestAdmissionMiddleware_AtCapacity_RejectsExcessWithServiceUnavailable(t *testing.T) {
	m := NewAdmissionMiddleware(2)
	release := make(chan struct{})
	var admitted int32

	blockingNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&admitted, 1)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := m.Handler(blockingNext)

	const n = 5
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}

	// Give every goroutine a chance to reach the middleware and either be
	// admitted (blocking on release) or rejected, before releasing —
	// otherwise an early request finishing could free a slot for a later
	// one, which would prove capacity works but not that excess is rejected
	// rather than queued.
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&admitted); got != 2 {
		t.Fatalf("expected exactly 2 requests admitted (capacity=2) before release, got %d", got)
	}
	close(release)
	wg.Wait()

	var okCount, rejectedCount int
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusServiceUnavailable:
			rejectedCount++
		default:
			t.Errorf("unexpected status code %d", code)
		}
	}
	if okCount != 2 {
		t.Errorf("admitted: got %d, want 2", okCount)
	}
	if rejectedCount != 3 {
		t.Errorf("rejected: got %d, want 3", rejectedCount)
	}
}

func TestAdmissionMiddleware_ReleasesSlotAfterCompletion(t *testing.T) {
	m := NewAdmissionMiddleware(1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := m.Handler(next)

	// Sequential, not concurrent: the first request must fully complete
	// (and release its slot) before the second is attempted, so a bug that
	// forgets to release would only show up on the second call.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got status %d, want 200 (slot should have been released after each prior request)", i, w.Code)
		}
	}
}
