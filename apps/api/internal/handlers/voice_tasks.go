package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

type CreateVoiceTaskRequest struct {
	RepositoryID string          `json:"repository_id"`
	Transcript   string          `json:"transcript"`
	Title        string          `json:"title,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

// CreateVoiceTask creates a task from a Whisper-style transcript payload.
func (h *Handler) CreateVoiceTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		respond.Error(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	var req CreateVoiceTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.RepositoryID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository_id is required"))
		return
	}
	if req.Transcript == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("transcript is required"))
		return
	}

	title := req.Title
	if title == "" {
		title = summarizeTranscript(req.Transcript)
	}
	if title == "" {
		title = "Voice task"
	}

	metadata := req.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	var metadataMap map[string]any
	if err := json.Unmarshal(metadata, &metadataMap); err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("metadata must be valid JSON"))
		return
	}
	if req.Provider == "" {
		req.Provider = "whisper"
	}
	metadataMap["voice_provider"] = req.Provider
	metadataMap["transcript"] = req.Transcript
	encodedMetadata, err := json.Marshal(metadataMap)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	task, err := h.insertTask(ctx, createTaskOptions{
		ProjectID:    projectID,
		RepositoryID: req.RepositoryID,
		CreatedBy:    user.UserID,
		Source:       integrationTypeVoice,
		Title:        title,
		Description:  req.Transcript,
		Metadata:     encodedMetadata,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	h.logAgentVaultEvent(ctx, taskCreatedEvent(task, integrationTypeVoice))
	respond.JSON(w, http.StatusCreated, task)
}
