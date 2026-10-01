package server

import (
	"net/http"
	"time"

	"github.com/ai-dev-control-plane/api/internal/webui"
)

// UnifiedHandler combines the existing Go API router with the embedded Vite
// application. API and health routes remain authoritative on the Go router.
func (s *Server) UnifiedHandler() http.Handler {
	return webui.Wrap(s.router)
}

// StartUnified starts the production single-origin server used by the Dev Plane
// binary. It intentionally uses the same timeout policy as Start.
func (s *Server) StartUnified(addr string) error {
	s.logger.Info("starting unified server", "addr", addr)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s.UnifiedHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s.httpSrv.ListenAndServe()
}
