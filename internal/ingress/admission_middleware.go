package ingress

import "net/http"

// AdmissionMiddleware caps aggregate in-flight requests independent of
// which model is requested — RFC Gap 13 (docs/rfc/0001-semantic-cache.md).
// Distinct from circuit breaking by design: circuit breaking rejects based
// on a specific model's observed failure rate regardless of system load;
// this rejects based on aggregate concurrency regardless of which model is
// requested. Found necessary via a load-test profiling pass where fixing a
// circuit-breaker correctness bug (conflating client cancellation with a
// genuine provider failure) removed an accidental crude load-shedding side
// effect the buggy behavior had been providing, with nothing built to
// replace it — the two are complementary, not redundant.
//
// Acquisition is non-blocking: at capacity, a request fails fast with 503
// rather than queuing, matching the RFC's own latency-budget constraint
// (a feature in the request path must degrade gracefully to a miss/
// pass-through, never to a hang).
type AdmissionMiddleware struct {
	sem chan struct{}
}

// NewAdmissionMiddleware creates a middleware admitting at most maxInFlight
// concurrent requests through whatever it wraps. maxInFlight <= 0 disables
// admission control entirely — Handler returns next unwrapped, so the
// unconfigured state is exactly the pre-feature state, per the RFC's own
// "features must degrade to equivalent-to-absent when not configured"
// constraint.
func NewAdmissionMiddleware(maxInFlight int) *AdmissionMiddleware {
	if maxInFlight <= 0 {
		return &AdmissionMiddleware{}
	}
	return &AdmissionMiddleware{sem: make(chan struct{}, maxInFlight)}
}

// Handler wraps next, admitting at most maxInFlight requests concurrently.
func (m *AdmissionMiddleware) Handler(next http.Handler) http.Handler {
	if m.sem == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
			next.ServeHTTP(w, r)
		default:
			writeError(w, http.StatusServiceUnavailable, "system_overloaded",
				"the system is at capacity; please retry shortly")
		}
	})
}
