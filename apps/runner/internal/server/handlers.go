package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/runtimes"
)

// Handler exposes the runtime Provider over HTTP.
type Handler struct {
	provider runtimes.Provider
	logger   *slog.Logger
}

// NewHandler creates a runner HTTP handler.
func NewHandler(provider runtimes.Provider, logger *slog.Logger) *Handler {
	return &Handler{provider: provider, logger: logger}
}

// RegisterRoutes mounts all runner routes on the given router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/workspaces", h.createWorkspace)
	r.Delete("/v1/workspaces/{sessionID}", h.destroyWorkspace)
	r.Post("/v1/workspaces/{sessionID}/commands", h.executeCommand)
	r.Get("/v1/workspaces/{sessionID}/files/*", h.readFile)
	r.Put("/v1/workspaces/{sessionID}/files/*", h.writeFile)
	r.Post("/v1/workspaces/{sessionID}/patches", h.applyPatch)
	r.Get("/v1/workspaces/{sessionID}/snapshot", h.snapshot)
	r.Post("/v1/workspaces/{sessionID}/restore", h.restore)
	r.Get("/v1/workspaces/{sessionID}/status", h.status)
	r.Get("/v1/workspaces/{sessionID}/logs", h.streamLogs)
}

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req runtimes.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}

	sess, err := h.provider.CreateWorkspace(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusCreated, sess)
}

func (h *Handler) destroyWorkspace(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if err := h.provider.DestroyWorkspace(r.Context(), sessionID); err != nil {
		if errors.Is(err, runtimes.ErrSessionNotFound) {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "destroyed"})
}

func (h *Handler) executeCommand(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	var cmd runtimes.Command
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Errorf("decode command: %w", err))
		return
	}

	result, err := h.provider.ExecuteCommand(r.Context(), sessionID, cmd)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) readFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	path := chi.URLParam(r, "*")
	path = strings.TrimPrefix(path, "/")

	data, err := h.provider.ReadFile(r.Context(), sessionID, path)
	if err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func (h *Handler) writeFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	path := chi.URLParam(r, "*")
	path = strings.TrimPrefix(path, "/")

	data, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}

	if err := h.provider.WriteFile(r.Context(), sessionID, path, data); err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "written"})
}

func (h *Handler) applyPatch(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}

	if err := h.provider.ApplyPatch(r.Context(), sessionID, string(body)); err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "patched"})
}

func (h *Handler) snapshot(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	snap, err := h.provider.Snapshot(r.Context(), sessionID)
	if err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, snap)
}

func (h *Handler) restore(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	var snap runtimes.Snapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Errorf("decode snapshot: %w", err))
		return
	}

	if err := h.provider.Restore(r.Context(), sessionID, &snap); err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	status, err := h.provider.GetStatus(r.Context(), sessionID)
	if err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	respondJSON(w, http.StatusOK, status)
}

func (h *Handler) streamLogs(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	lines, err := h.provider.StreamLogs(r.Context(), sessionID)
	if err != nil {
		if err == runtimes.ErrSessionNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return
			}
			data, err := json.Marshal(line)
			if err != nil {
				h.logger.Error("failed to marshal log line", "error", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Default().Error("failed to encode response", "error", err)
	}
}

func respondError(w http.ResponseWriter, status int, err error) {
	respondJSON(w, status, map[string]string{"error": err.Error()})
}
