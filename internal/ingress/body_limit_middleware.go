package ingress

import "net/http"

// BodyLimitMiddleware caps the size of every inbound request body. Nothing
// in the ingress layer enforced this before — every JSON-decoding handler
// and middleware (ChatHandler, EmbeddingsHandler, ImagesHandler,
// InterceptorMiddleware, MemoryMiddleware, SessionMiddleware,
// CacheMiddleware) reads r.Body directly with no limit of its own, and
// several of them re-decode/re-marshal the same body more than once as it
// passes through the chain (interceptors mutate the request, then
// SessionMiddleware and MemoryMiddleware each prepend to it and re-encode)
// — multiplying the memory/CPU cost of a single oversized request rather
// than just paying it once.
//
// Wraps the whole mux (see pkg/ixr/ixr.go) rather than each handler
// individually, so a new endpoint added later is covered automatically
// without remembering to apply this at every registration site.
type BodyLimitMiddleware struct {
	maxBytes int64
}

// NewBodyLimitMiddleware creates a middleware capping every request body at
// maxBytes. maxBytes <= 0 disables the cap entirely — Handler returns next
// unwrapped, so the disabled state is exactly the pre-feature behavior.
func NewBodyLimitMiddleware(maxBytes int64) *BodyLimitMiddleware {
	return &BodyLimitMiddleware{maxBytes: maxBytes}
}

// Handler wraps next, capping every request's body via http.MaxBytesReader.
// A read past the limit fails with an error at the point something tries
// to read past it (json.Decode, in every current caller) — every existing
// decode-error branch already treats a failed Decode as a 400
// invalid_request_body, so this needs no changes to individual handlers
// to take effect; it only needs to set the limit.
func (m *BodyLimitMiddleware) Handler(next http.Handler) http.Handler {
	if m.maxBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes)
		next.ServeHTTP(w, r)
	})
}
