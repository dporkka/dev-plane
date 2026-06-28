// Package server provides the HTTP server for the runner service.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ai-dev-control-plane/runner/internal/config"
	"github.com/ai-dev-control-plane/runtimes"
)

// Server wraps the runner HTTP server and provider.
type Server struct {
	cfg      *config.Config
	provider runtimes.Provider
	logger   *slog.Logger
	httpSrv  *http.Server
}

// New creates a runner server.
func New(cfg *config.Config, provider runtimes.Provider, logger *slog.Logger) *Server {
	return &Server{
		cfg:      cfg,
		provider: provider,
		logger:   logger,
	}
}

// Start configures routes and starts the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	if s.cfg.AuthToken != "" {
		r.Use(s.authMiddleware)
	}

	// Health checks
	r.Get("/health", s.healthCheck)
	r.Get("/ready", s.readyCheck)

	// Runtime API
	handler := NewHandler(s.provider, s.logger)
	handler.RegisterRoutes(r)

	s.httpSrv = &http.Server{
		Addr:         s.cfg.Address(),
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.logger.Info("runner HTTP server starting", "addr", s.cfg.Address())

	// Run server in a goroutine so we can wait for ctx cancellation.
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	}
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, config.ShutdownTimeout())
	defer cancel()

	s.logger.Info("runner HTTP server shutting down")
	if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown runner server: %w", err)
	}

	if closer, ok := s.provider.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			s.logger.Warn("failed to close runtime provider", "error", err)
		}
	}

	return nil
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) < len(prefix) || auth[:len(prefix)] != prefix {
			http.Error(w, `{"error":"missing or invalid authorization"}`, http.StatusUnauthorized)
			return
		}
		if auth[len(prefix):] != s.cfg.AuthToken {
			http.Error(w, `{"error":"invalid authorization"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) readyCheck(w http.ResponseWriter, r *http.Request) {
	// Provider readiness is implicit: if we can get status of a non-existent
	// session without an internal error, the provider is reachable.
	_, err := s.provider.GetStatus(r.Context(), "__runner_ready_check__")
	if err != nil && !errors.Is(err, runtimes.ErrSessionNotFound) {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not ready",
			"reason": err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
