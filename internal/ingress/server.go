package ingress

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Server is the HTTP listener for ixr's ingress.
// It knows nothing about providers or domain logic — only HTTP.
type Server struct {
	port   int
	mux    *http.ServeMux
	tlsCfg *tls.Config // nil = plain HTTP
}

// NewServer creates a plain HTTP server.
func NewServer(port int, mux *http.ServeMux) *Server {
	return &Server{port: port, mux: mux}
}

// Run starts listening and blocks until ctx is cancelled or a fatal error occurs.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
		TLSConfig:    s.tlsCfg,
	}

	errCh := make(chan error, 1)
	go func() {
		scheme := "http"
		if s.tlsCfg != nil {
			scheme = "https"
		}
		slog.Info("ixr listening", "port", s.port, "scheme", scheme)
		var err error
		if s.tlsCfg != nil {
			err = srv.ListenAndServeTLS("", "") // cert already loaded in TLSConfig
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
