package ingress

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// CacheMiddleware wraps a ChatHandler and serves non-streaming requests from the
// response cache when available. It accepts a RequestAwareCache so both
// exact-match and semantic backends can be used interchangeably.
type CacheMiddleware struct {
	cache  cache.RequestAwareCache
	ttl    time.Duration
	next   http.Handler
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

	// Cache miss — capture the response so we can store it.
	w.Header().Set("X-Cache", "MISS")
	rec := &responseRecorder{ResponseWriter: w, headerCode: http.StatusOK}
	r.Body = body.replay()
	m.next.ServeHTTP(rec, r)

	if rec.headerCode == http.StatusOK && len(rec.body) > 0 {
		var resp schema.ResponseEnvelope
		if err := json.Unmarshal(rec.body, &resp); err == nil {
			m.cache.Store(r.Context(), &req, &resp, m.ttl)
		}
	}
}

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
type bytesReader struct{ data []byte; pos int }

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
