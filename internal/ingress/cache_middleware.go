package ingress

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// CacheMiddleware wraps a ChatHandler and serves non-streaming requests from the
// response cache when available. It accepts a RequestAwareCache so both
// exact-match and semantic backends can be used interchangeably.
type CacheMiddleware struct {
	cache cache.RequestAwareCache
	ttl   time.Duration
	next  http.Handler

	// group coalesces concurrent identical cache misses into one upstream
	// call — without it, a burst of simultaneous identical requests each
	// independently reaches the provider, even though only the request that
	// triggers the call needs to. Zero value is ready to use.
	group singleflight.Group
}

// NewCacheMiddleware returns a caching wrapper around next.
// ttl=0 uses the cache's default TTL.
func NewCacheMiddleware(c cache.RequestAwareCache, ttl time.Duration, next http.Handler) *CacheMiddleware {
	return &CacheMiddleware{cache: c, ttl: ttl, next: next}
}

func (m *CacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only cache non-streaming POST requests.
	if r.Method != http.MethodPost {
		m.next.ServeHTTP(w, r)
		return
	}

	// Peek at the body to decode the request without consuming it.
	var req schema.RequestEnvelope
	body := &bodyCapture{ReadCloser: r.Body}
	if err := json.NewDecoder(body).Decode(&req); err != nil || req.Stream {
		// Can't cache streaming requests or malformed bodies — pass through.
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}

	if resp, hit, ok := m.cache.Lookup(r.Context(), &req); ok {
		slog.Debug("cache hit", "key", cache.Key(&req)[:8], "layer", hit)
		w.Header().Set("X-Cache", hit.String())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Cache miss — coalesce concurrent identical misses via singleflight,
	// keyed the same way the cache itself is keyed. Only the caller
	// singleflight elects to actually run the closure reaches the
	// provider; everyone else (including that same caller) just waits on
	// its result. The closure writes into its own detached recorder rather
	// than any specific caller's real http.ResponseWriter — after Do
	// returns, every waiter (leader and followers alike) copies the shared
	// result onto its own w. Store happens exactly once, inside the
	// closure, rather than once per waiter.
	//
	// Known limitation, inherent to singleflight: the closure runs with
	// whichever caller's context singleflight happened to start it under.
	// If that specific caller disconnects, every other waiter's response
	// is cancelled too, even though they're still connected. Acceptable for
	// a first cut — this is the standard, documented behavior of
	// golang.org/x/sync/singleflight, not a bug specific to this code.
	replayBody := body.buf
	key := cache.Key(&req)
	v, _, shared := m.group.Do(key, func() (any, error) {
		innerReq := r.Clone(r.Context())
		innerReq.Body = readCloser(replayBody)

		rec := &responseRecorder{ResponseWriter: newDiscardResponseWriter(), headerCode: http.StatusOK}
		m.next.ServeHTTP(rec, innerReq)

		if rec.headerCode == http.StatusOK && len(rec.body) > 0 {
			var resp schema.ResponseEnvelope
			if err := json.Unmarshal(rec.body, &resp); err == nil {
				m.cache.Store(r.Context(), &req, &resp, m.ttl)
			}
		}
		return coalescedResponse{status: rec.headerCode, body: rec.body}, nil
	})

	cr := v.(coalescedResponse)
	cacheStatus := "MISS"
	if shared {
		cacheStatus = "COALESCED"
	}
	w.Header().Set("X-Cache", cacheStatus)
	w.WriteHeader(cr.status)
	_, _ = w.Write(cr.body)
}

// coalescedResponse is what the singleflight closure returns — every
// waiter (leader and followers) writes this onto its own ResponseWriter.
type coalescedResponse struct {
	status int
	body   []byte
}

// discardResponseWriter absorbs writes made inside the singleflight
// closure. It isn't connected to any real HTTP client — the actual
// response reaches each caller afterward, via coalescedResponse.
type discardResponseWriter struct{ header http.Header }

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}

func (d *discardResponseWriter) Header() http.Header         { return d.header }
func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardResponseWriter) WriteHeader(int)             {}

// bodyCapture records the bytes read from an http.Request.Body so they can be replayed.
type bodyCapture struct {
	io.ReadCloser
	buf []byte
}

func (b *bodyCapture) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.buf = append(b.buf, p[:n]...)
	return n, err
}

func (b *bodyCapture) replay() io.ReadCloser {
	return readCloser(b.buf)
}

type nopCloser struct{ *bytesReader }
type bytesReader struct {
	data []byte
	pos  int
}

func (br *bytesReader) Read(p []byte) (int, error) {
	if br.pos >= len(br.data) {
		return 0, nil
	}
	n := copy(p, br.data[br.pos:])
	br.pos += n
	return n, nil
}

func readCloser(data []byte) io.ReadCloser {
	return &nopCloser{&bytesReader{data: data}}
}
func (n *nopCloser) Close() error { return nil }

// responseRecorder captures the response body and status code.
type responseRecorder struct {
	http.ResponseWriter
	headerCode int
	body       []byte
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.headerCode = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	rr.body = append(rr.body, b...)
	return rr.ResponseWriter.Write(b)
}
