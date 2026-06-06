package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/respond"
)

// Artifact represents a stored artifact (screenshot, log file, etc.).
type Artifact struct {
	ID          string          `json:"id"`
	AgentRunID  *string         `json:"agent_run_id,omitempty"`
	StepID      *string         `json:"step_id,omitempty"`
	ArtifactType string         `json:"artifact_type"`
	FileName    string          `json:"file_name"`
	FilePath    string          `json:"file_path"`
	MimeType    string          `json:"mime_type,omitempty"`
	SizeBytes   int64           `json:"size_bytes,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// GetArtifact serves an artifact file by ID.
// It looks up the artifact in the database, verifies access, and streams the file.
func (h *Handler) GetArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	artifactID := chi.URLParam(r, "id")
	if artifactID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("artifact id is required"))
		return
	}

	var artifact Artifact
	var agentRunID, stepID sql.NullString
	var mimeType sql.NullString
	var sizeBytes sql.NullInt64
	var metadata sql.NullString

	err := h.db.QueryRowContext(ctx, `
		SELECT id, agent_run_id, step_id, artifact_type, file_name, file_path,
		       mime_type, size_bytes, metadata, created_at
		FROM artifacts
		WHERE id = $1
	`, artifactID).Scan(
		&artifact.ID, &agentRunID, &stepID, &artifact.ArtifactType,
		&artifact.FileName, &artifact.FilePath, &mimeType, &sizeBytes,
		&metadata, &artifact.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("artifact not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if agentRunID.Valid {
		artifact.AgentRunID = &agentRunID.String
	}
	if stepID.Valid {
		artifact.StepID = &stepID.String
	}
	if mimeType.Valid {
		artifact.MimeType = mimeType.String
	}
	if sizeBytes.Valid {
		artifact.SizeBytes = sizeBytes.Int64
	}
	if metadata.Valid {
		artifact.Metadata = json.RawMessage(metadata.String)
	}

	// Determine the full file path
	// Check if artifact is stored in configured artifacts dir or as an absolute path
	artifactsDir := h.getArtifactsDir()
	fullPath := artifact.FilePath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(artifactsDir, fullPath)
	}

	// If file doesn't exist, return artifact metadata as JSON
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		respond.JSON(w, http.StatusOK, artifact)
		return
	}

	// Determine mime type if not set
	contentType := artifact.MimeType
	if contentType == "" {
		contentType = detectMimeType(fullPath, artifact.FileName)
	}

	// Serve the file
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, artifact.FileName))
	if artifact.SizeBytes > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", artifact.SizeBytes))
	}
	http.ServeFile(w, r, fullPath)
}

// getArtifactsDir returns the base directory for artifact storage.
// Defaults to ./artifacts relative to working directory.
func (h *Handler) getArtifactsDir() string {
	if dir := os.Getenv("ARTIFACTS_DIR"); dir != "" {
		return dir
	}
	return "./artifacts"
}

// detectMimeType determines the MIME type from file extension or path.
func detectMimeType(filePath, fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(filePath))
	}
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".log":
		return "text/plain"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".md":
		return "text/markdown"
	case ".xml":
		return "application/xml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	default:
		return "application/octet-stream"
	}
}
