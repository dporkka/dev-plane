// Package handlers provides HTTP handlers for the API service.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ai-dev-control-plane/api/internal/respond"
)

// HealthHandler provides health and readiness check endpoints.
type HealthHandler struct {
	db *sql.DB
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(db *sql.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// HealthCheck responds with a simple health status.
func (h *HealthHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// ReadyCheck checks database connectivity and responds with readiness status.
func (h *HealthHandler) ReadyCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, err)
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "ready",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// Legacy health check functions for direct use in router setup.
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func ReadyCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ready",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}
