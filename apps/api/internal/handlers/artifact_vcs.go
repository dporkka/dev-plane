package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	maxArtifactUploadBytes int64 = 50 << 30
	artifactUploadTimeout        = 2 * time.Hour
)

type ArtifactMetadataResponse struct {
	ID             string                  `json:"id"`
	OrganizationID string                  `json:"organization_id"`
	WorkspaceID    *string                 `json:"workspace_id,omitempty"`
	ArtifactType   string                  `json:"artifact_type"`
	FileName       string                  `json:"file_name"`
	LogicalPath    string                  `json:"logical_path"`
	Kind           artifactstore.Kind      `json:"kind"`
	MimeType       string                  `json:"mime_type,omitempty"`
	SizeBytes      int64                   `json:"size_bytes"`
	Digest         artifactstore.Digest    `json:"digest"`
	SemanticDigest *artifactstore.Digest   `json:"semantic_digest,omitempty"`
	Metadata       map[string]string       `json:"metadata,omitempty"`
	Derivatives    []artifactstore.DerivativeRef `json:"derivatives,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
}

type artifactRecord struct {
	ID             string
	OrganizationID string
	WorkspaceID    sql.NullString
	ArtifactType   string
	FileName       string
	LogicalPath    sql.NullString
	MimeType       sql.NullString
	SizeBytes      sql.NullInt64
	ArtifactJSON   sql.NullString
	Metadata       sql.NullString
	CreatedAt      time.Time
}

func (h *Handler) UploadWorkspaceArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactManager == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("artifact storage is not configured"))
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	logicalPath, err := artifactstore.NormalizeArtifactPath(r.URL.Query().Get("path"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if existing, existingErr := h.latestWorkspaceArtifact(ctx, workspaceID, logicalPath); existingErr == nil {
		policy := artifactstore.DefaultCollaborationPolicy(existing)
		if policy.LeaseRequired {
			if h.artifactLeases == nil {
				respond.Error(w, http.StatusLocked, errors.New("artifact update requires a write lease"))
				return
			}
			token := strings.TrimSpace(r.Header.Get("X-Artifact-Lease-Token"))
			generation, parseErr := strconv.ParseUint(strings.TrimSpace(r.Header.Get("X-Artifact-Lease-Generation")), 10, 64)
			if token == "" || parseErr != nil || generation == 0 {
				respond.Error(w, http.StatusLocked, errors.New("artifact update requires X-Artifact-Lease-Token and X-Artifact-Lease-Generation"))
				return
			}
			if err := h.artifactLeases.Validate(ctx, artifactstore.Lease{
				ScopeID: workspaceID,
				Path: logicalPath,
				OwnerID: user.UserID,
				Token: token,
				Generation: generation,
			}); err != nil {
				respondArtifactLeaseError(w, err)
				return
			}
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		respond.Error(w, http.StatusInternalServerError, existingErr)
		return
	}

	chunker, err := artifactstore.NewContentDefinedChunker(artifactstore.DefaultChunkingConfig())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(artifactUploadTimeout))
	_ = controller.SetWriteDeadline(time.Now().Add(artifactUploadTimeout))

	body := http.MaxBytesReader(w, r.Body, maxArtifactUploadBytes)
	defer body.Close()

	artifact, err := h.artifactManager.PutChunkedArtifact(ctx, chunker, artifactstore.PutArtifactRequest{
		Path:      logicalPath,
		Kind:      artifactstore.KindBinary,
		MediaType: contentType,
		Metadata: map[string]string{
			"workspace_id": workspaceID,
			"uploaded_by":  user.UserID,
		},
		Reader: body,
	})
	if err != nil {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("store artifact: %w", err))
		return
	}

	var analysis artifactstore.AdapterAnalysis
	if shouldAnalyzeArtifact(logicalPath, contentType) && h.artifactRegistry != nil {
		analyzed, result, analysisErr := h.artifactManager.AnalyzeArtifact(ctx, h.artifactRegistry, artifact, artifactstore.AnalyzeOptions{})
		if analysisErr != nil {
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]string{}
			}
			artifact.Metadata["analysis_error"] = analysisErr.Error()
		} else {
			artifact = analyzed
			analysis = result
		}
	}

	record, err := h.persistArtifact(ctx, user.OrgID, workspaceID, artifact, analysis)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusCreated, record)
}

func shouldAnalyzeArtifact(logicalPath, mediaType string) bool {
	ext := strings.ToLower(path.Ext(logicalPath))
	switch ext {
	case ".docx", ".xlsx", ".pptx", ".pdf", ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return mediaType == artifactstore.MediaTypeDOCX ||
		mediaType == artifactstore.MediaTypeXLSX ||
		mediaType == artifactstore.MediaTypePPTX ||
		mediaType == "application/pdf" ||
		strings.HasPrefix(mediaType, "image/")
}

func (h *Handler) persistArtifact(
	ctx context.Context,
	orgID, workspaceID string,
	artifact artifactstore.Artifact,
	analysis artifactstore.AdapterAnalysis,
) (ArtifactMetadataResponse, error) {
	id := uuid.NewString()
	now := time.Now().UTC()
	payload, err := json.Marshal(artifact)
	if err != nil {
		return ArtifactMetadataResponse{}, fmt.Errorf("marshal artifact: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"artifact": artifact.Metadata,
		"analysis": map[string]any{
			"adapter":  analysis.Adapter,
			"version":  analysis.Version,
			"warnings": analysis.Warnings,
		},
	})
	if err != nil {
		return ArtifactMetadataResponse{}, fmt.Errorf("marshal artifact metadata: %w", err)
	}

	var semanticAlgorithm, semanticHex any
	if artifact.SemanticDigest != nil {
		semanticAlgorithm = artifact.SemanticDigest.Algorithm
		semanticHex = artifact.SemanticDigest.Hex
	}
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO artifacts (
			id, organization_id, workspace_id, artifact_type, file_name, file_path,
			logical_path, kind, mime_type, size_bytes, digest_algorithm, digest_hex,
			semantic_digest_algorithm, semantic_digest_hex, artifact_json, metadata, created_at
		) VALUES (
			$1, $2, $3, 'workspace_file', $4, $5,
			$6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16
		)
	`,
		id, orgID, workspaceID, path.Base(artifact.Path), artifact.Path,
		artifact.Path, string(artifact.Kind), artifact.Descriptor.MediaType,
		artifact.Descriptor.Size, artifact.Descriptor.Digest.Algorithm, artifact.Descriptor.Digest.Hex,
		semanticAlgorithm, semanticHex, string(payload), string(metadata), now,
	)
	if err != nil {
		return ArtifactMetadataResponse{}, fmt.Errorf("persist artifact: %w", err)
	}
	return artifactResponse(id, orgID, &workspaceID, artifact, now), nil
}

func artifactResponse(id, orgID string, workspaceID *string, artifact artifactstore.Artifact, createdAt time.Time) ArtifactMetadataResponse {
	return ArtifactMetadataResponse{
		ID:             id,
		OrganizationID: orgID,
		WorkspaceID:    workspaceID,
		ArtifactType:   "workspace_file",
		FileName:       path.Base(artifact.Path),
		LogicalPath:    artifact.Path,
		Kind:           artifact.Kind,
		MimeType:       artifact.Descriptor.MediaType,
		SizeBytes:      artifact.Descriptor.Size,
		Digest:         artifact.Descriptor.Digest,
		SemanticDigest: artifact.SemanticDigest,
		Metadata:       artifact.Metadata,
		Derivatives:    artifact.Derivatives,
		CreatedAt:      createdAt,
	}
}

func (h *Handler) GetArtifactMetadata(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	artifactID := chi.URLParam(r, "id")
	if err := authz.AuthorizeArtifact(ctx, h.db, user, artifactID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("artifact not found"))
		return
	}
	record, artifact, err := h.loadCASArtifact(ctx, artifactID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("artifact not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	var workspaceID *string
	if record.WorkspaceID.Valid {
		workspaceID = &record.WorkspaceID.String
	}
	respond.JSON(w, http.StatusOK, artifactResponse(
		record.ID, record.OrganizationID, workspaceID, artifact, record.CreatedAt,
	))
}

func (h *Handler) MaterializeArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactManager == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("artifact storage is not configured"))
		return
	}
	artifactID := chi.URLParam(r, "id")
	if err := authz.AuthorizeArtifact(ctx, h.db, user, artifactID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("artifact not found"))
		return
	}
	record, artifact, err := h.loadCASArtifact(ctx, artifactID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("artifact not found"))
		return
	}
	contentType := artifact.Descriptor.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", artifact.Descriptor.Size))
	w.Header().Set("ETag", `"`+artifact.Descriptor.Digest.String()+`"`)
	filename := strings.ReplaceAll(strings.ReplaceAll(record.FileName, "\r", ""), "\n", "")
	filename = strings.ReplaceAll(filename, `"`, "")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	if err := h.artifactManager.Materialize(ctx, artifact, w); err != nil {
		h.logger.Error("materialize artifact", "artifact_id", artifactID, "error", err)
	}
}

func (h *Handler) latestWorkspaceArtifact(ctx context.Context, workspaceID, logicalPath string) (artifactstore.Artifact, error) {
	var (
		raw       sql.NullString
		tombstone bool
	)
	err := h.db.QueryRowContext(ctx, `
		SELECT artifact_json, is_tombstone
		FROM artifacts
		WHERE workspace_id = $1 AND logical_path = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, logicalPath).Scan(&raw, &tombstone)
	if err != nil {
		return artifactstore.Artifact{}, err
	}
	if tombstone || !raw.Valid {
		return artifactstore.Artifact{}, sql.ErrNoRows
	}
	var artifact artifactstore.Artifact
	if err := json.Unmarshal([]byte(raw.String), &artifact); err != nil {
		return artifactstore.Artifact{}, fmt.Errorf("decode latest workspace artifact: %w", err)
	}
	return artifact, nil
}

func (h *Handler) loadCASArtifact(ctx context.Context, artifactID string) (artifactRecord, artifactstore.Artifact, error) {
	var record artifactRecord
	err := h.db.QueryRowContext(ctx, `
		SELECT id, organization_id, workspace_id, artifact_type, file_name,
		       logical_path, mime_type, size_bytes, artifact_json, metadata, created_at
		FROM artifacts
		WHERE id = $1
	`, artifactID).Scan(
		&record.ID, &record.OrganizationID, &record.WorkspaceID, &record.ArtifactType,
		&record.FileName, &record.LogicalPath, &record.MimeType, &record.SizeBytes,
		&record.ArtifactJSON, &record.Metadata, &record.CreatedAt,
	)
	if err != nil {
		return artifactRecord{}, artifactstore.Artifact{}, err
	}
	if !record.ArtifactJSON.Valid || strings.TrimSpace(record.ArtifactJSON.String) == "" {
		return record, artifactstore.Artifact{}, errors.New("artifact has no CAS representation")
	}
	var artifact artifactstore.Artifact
	if err := json.Unmarshal([]byte(record.ArtifactJSON.String), &artifact); err != nil {
		return record, artifactstore.Artifact{}, fmt.Errorf("decode artifact state: %w", err)
	}
	return record, artifact, nil
}

type ArtifactDiffRequest struct {
	BeforeID string `json:"before_id"`
	AfterID  string `json:"after_id"`
}

func (h *Handler) DiffArtifacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactManager == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("artifact storage is not configured"))
		return
	}
	var req ArtifactDiffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.BeforeID == "" || req.AfterID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("before_id and after_id are required"))
		return
	}
	if err := authz.AuthorizeArtifact(ctx, h.db, user, req.BeforeID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("before artifact not found"))
		return
	}
	if err := authz.AuthorizeArtifact(ctx, h.db, user, req.AfterID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("after artifact not found"))
		return
	}
	_, before, err := h.loadCASArtifact(ctx, req.BeforeID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	_, after, err := h.loadCASArtifact(ctx, req.AfterID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	diff, err := h.artifactManager.DiffArtifacts(ctx, before, after)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusOK, diff)
}

type ArtifactLeaseRequest struct {
	Path       string `json:"path"`
	Token      string `json:"token,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

func (h *Handler) AcquireArtifactLease(w http.ResponseWriter, r *http.Request) {
	h.handleArtifactLease(w, r, "acquire")
}

func (h *Handler) RenewArtifactLease(w http.ResponseWriter, r *http.Request) {
	h.handleArtifactLease(w, r, "renew")
}

func (h *Handler) ReleaseArtifactLease(w http.ResponseWriter, r *http.Request) {
	h.handleArtifactLease(w, r, "release")
}

func (h *Handler) handleArtifactLease(w http.ResponseWriter, r *http.Request, operation string) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactLeases == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("artifact leases are not configured"))
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}
	var req ArtifactLeaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	var (
		lease artifactstore.Lease
		err   error
	)
	switch operation {
	case "acquire":
		lease, err = h.artifactLeases.Acquire(ctx, artifactstore.AcquireLeaseRequest{
			ScopeID: workspaceID, Path: req.Path, OwnerID: user.UserID, TTL: ttl,
		})
	case "renew":
		lease, err = h.artifactLeases.Renew(ctx, artifactstore.Lease{
			ScopeID: workspaceID, Path: req.Path, OwnerID: user.UserID,
			Token: req.Token, Generation: req.Generation,
		}, ttl)
	case "release":
		err = h.artifactLeases.Release(ctx, artifactstore.Lease{
			ScopeID: workspaceID, Path: req.Path, OwnerID: user.UserID,
			Token: req.Token, Generation: req.Generation,
		})
		if err == nil {
			respond.JSON(w, http.StatusOK, map[string]string{"status": "released"})
			return
		}
	}
	if err != nil {
		respondArtifactLeaseError(w, err)
		return
	}
	respond.JSON(w, http.StatusOK, lease)
}

func (h *Handler) GetArtifactLease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactLeases == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("artifact leases are not configured"))
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}
	lease, err := h.artifactLeases.Get(ctx, workspaceID, r.URL.Query().Get("path"))
	if err != nil {
		respondArtifactLeaseError(w, err)
		return
	}
	respond.JSON(w, http.StatusOK, lease)
}

func respondArtifactLeaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, artifactstore.ErrLeaseHeld):
		respond.Error(w, http.StatusLocked, err)
	case errors.Is(err, artifactstore.ErrLeaseNotFound):
		respond.Error(w, http.StatusNotFound, err)
	case errors.Is(err, artifactstore.ErrLeaseExpired), errors.Is(err, artifactstore.ErrLeaseToken):
		respond.Error(w, http.StatusConflict, err)
	default:
		respond.Error(w, http.StatusBadRequest, err)
	}
}

type WorkspaceSnapshotRequest struct {
	Description string `json:"description,omitempty"`
}

type WorkspaceSnapshotResponse struct {
	ID                     string    `json:"id"`
	WorkspaceID            string    `json:"workspace_id"`
	GitCommit              string    `json:"git_commit,omitempty"`
	VCSChangeID            string    `json:"vcs_change_id,omitempty"`
	ArtifactManifestDigest string    `json:"artifact_manifest_digest,omitempty"`
	ArtifactVersionDigest  string    `json:"artifact_version_digest,omitempty"`
	Description            string    `json:"description,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

func (h *Handler) CreateWorkspaceSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}
	var req WorkspaceSnapshotRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = "workspace snapshot"
	}

	sourceSnapshot, err := h.captureWorkspaceSourceSnapshot(ctx, workspaceID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	manifestDigest, versionDigest, err := h.buildWorkspaceArtifactVersion(ctx, workspaceID, user.UserID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO workspace_snapshots (
			id, workspace_id, git_commit, vcs_change_id,
			artifact_manifest_digest, artifact_version_digest,
			description, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, '{}', $8)
	`,
		id, workspaceID,
		nullIfEmpty(sourceSnapshot.GitCommit), nullIfEmpty(sourceSnapshot.VCSChangeID),
		nullIfEmpty(manifestDigest), nullIfEmpty(versionDigest),
		description, now,
	)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist workspace snapshot: %w", err))
		return
	}
	respond.JSON(w, http.StatusCreated, WorkspaceSnapshotResponse{
		ID: id, WorkspaceID: workspaceID,
		GitCommit: sourceSnapshot.GitCommit, VCSChangeID: sourceSnapshot.VCSChangeID,
		ArtifactManifestDigest: manifestDigest, ArtifactVersionDigest: versionDigest,
		Description: description, CreatedAt: now,
	})
}

func (h *Handler) captureWorkspaceSourceSnapshot(ctx context.Context, workspaceID string) (*runtimes.Snapshot, error) {
	workspace, provider, err := h.getRuntimeWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if provider != nil && workspace.RuntimeSessionID != nil {
		return provider.Snapshot(ctx, *workspace.RuntimeSessionID)
	}
	if workspace.WorktreePath == nil || *workspace.WorktreePath == "" {
		return nil, errors.New("workspace has no source-control runtime")
	}
	worktree := *workspace.WorktreePath
	if out, err := exec.CommandContext(ctx, "git", "-C", worktree, "add", "-A").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git add: %w: %s", err, out)
	}
	cmd := exec.CommandContext(
		ctx, "git", "-C", worktree,
		"-c", "user.email=dev-plane@example.invalid",
		"-c", "user.name=Dev Plane",
		"commit", "-m", "snapshot: dev-plane", "--allow-empty",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git commit snapshot: %w: %s", err, out)
	}
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse: %w: %s", err, out)
	}
	commit := strings.TrimSpace(string(out))
	return &runtimes.Snapshot{ID: commit, SessionID: workspaceID, GitCommit: commit, CreatedAt: time.Now().UTC()}, nil
}

func (h *Handler) currentWorkspaceArtifactTree(ctx context.Context, workspaceID string) (map[string]artifactstore.Artifact, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT logical_path, is_tombstone, artifact_json
		FROM artifacts
		WHERE workspace_id = $1 AND logical_path IS NOT NULL
		ORDER BY created_at ASC, id ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace artifact history: %w", err)
	}
	defer rows.Close()

	latest := map[string]artifactstore.Artifact{}
	for rows.Next() {
		var (
			logicalPath string
			tombstone   bool
			raw         sql.NullString
		)
		if err := rows.Scan(&logicalPath, &tombstone, &raw); err != nil {
			return nil, err
		}
		if tombstone {
			delete(latest, logicalPath)
			continue
		}
		if !raw.Valid {
			continue
		}
		var artifact artifactstore.Artifact
		if err := json.Unmarshal([]byte(raw.String), &artifact); err != nil {
			return nil, fmt.Errorf("decode workspace artifact %q: %w", logicalPath, err)
		}
		latest[logicalPath] = artifact
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return latest, nil
}

func (h *Handler) buildWorkspaceArtifactVersion(ctx context.Context, workspaceID, authorID string) (string, string, error) {
	if h.artifactManager == nil {
		return "", "", nil
	}
	latest, err := h.currentWorkspaceArtifactTree(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	if len(latest) == 0 {
		return "", "", nil
	}

	items := make([]artifactstore.Artifact, 0, len(latest))
	for _, artifact := range latest {
		items = append(items, artifact)
	}
	manifest, err := artifactstore.NewManifest(items)
	if err != nil {
		return "", "", err
	}
	manifestDescriptor, err := h.artifactManager.PutManifest(ctx, manifest)
	if err != nil {
		return "", "", err
	}

	var parents []artifactstore.Digest
	var previous sql.NullString
	err = h.db.QueryRowContext(ctx, `
		SELECT artifact_version_digest
		FROM workspace_snapshots
		WHERE workspace_id = $1 AND artifact_version_digest IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if previous.Valid {
		if digest, parseErr := artifactstore.ParseDigest(previous.String); parseErr == nil {
			parents = append(parents, digest)
		}
	}
	version := artifactstore.Version{
		Schema:    artifactstore.VersionSchemaV1,
		Parents:   parents,
		Manifest:  manifestDescriptor.Digest,
		Author:    artifactstore.ActorIdentity{ID: authorID, Type: "human"},
		Message:   "workspace snapshot",
		CreatedAt: time.Now().UTC(),
		Provenance: artifactstore.Provenance{WorkspaceID: workspaceID},
	}
	versionDescriptor, err := h.artifactManager.PutVersion(ctx, version)
	if err != nil {
		return "", "", err
	}
	return manifestDescriptor.Digest.String(), versionDescriptor.Digest.String(), nil
}

func (h *Handler) ListWorkspaceArtifacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, organization_id, artifact_json, created_at, logical_path, is_tombstone
		FROM artifacts
		WHERE workspace_id = $1 AND logical_path IS NOT NULL
		ORDER BY created_at ASC, id ASC
	`, workspaceID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	type currentItem struct {
		id        string
		orgID     string
		artifact  artifactstore.Artifact
		createdAt time.Time
	}
	latest := map[string]currentItem{}
	for rows.Next() {
		var (
			id, orgID, logicalPath string
			raw                    sql.NullString
			createdAt              time.Time
			tombstone              bool
		)
		if err := rows.Scan(&id, &orgID, &raw, &createdAt, &logicalPath, &tombstone); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if tombstone {
			delete(latest, logicalPath)
			continue
		}
		if !raw.Valid {
			continue
		}
		var artifact artifactstore.Artifact
		if err := json.Unmarshal([]byte(raw.String), &artifact); err != nil {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("decode workspace artifact %q: %w", logicalPath, err))
			return
		}
		latest[logicalPath] = currentItem{id: id, orgID: orgID, artifact: artifact, createdAt: createdAt}
	}
	if err := rows.Err(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	result := make([]ArtifactMetadataResponse, 0, len(latest))
	for _, item := range latest {
		workspace := workspaceID
		result = append(result, artifactResponse(item.id, item.orgID, &workspace, item.artifact, item.createdAt))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LogicalPath < result[j].LogicalPath })
	respond.JSON(w, http.StatusOK, result)
}
func (h *Handler) DeleteWorkspaceArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}
	logicalPath, err := artifactstore.NormalizeArtifactPath(r.URL.Query().Get("path"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	existing, err := h.latestWorkspaceArtifact(ctx, workspaceID, logicalPath)
	if errors.Is(err, sql.ErrNoRows) {
		respond.Error(w, http.StatusNotFound, errors.New("artifact path not found"))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if policy := artifactstore.DefaultCollaborationPolicy(existing); policy.LeaseRequired {
		if err := h.validateArtifactLeaseHeaders(ctx, r, workspaceID, logicalPath, user.UserID); err != nil {
			respondArtifactLeaseError(w, err)
			return
		}
	}
	now := time.Now().UTC()
	metadata, _ := json.Marshal(map[string]any{
		"deleted_by": user.UserID,
		"previous_digest": existing.Descriptor.Digest.String(),
	})
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO artifacts (
			id, organization_id, workspace_id, artifact_type, file_name, file_path,
			logical_path, kind, mime_type, size_bytes, is_tombstone, metadata, created_at
		) VALUES ($1, $2, $3, 'tombstone', $4, $5, $6, 'tombstone', $7, 0, true, $8, $9)
	`, uuid.NewString(), user.OrgID, workspaceID, path.Base(logicalPath), logicalPath,
		logicalPath, existing.Descriptor.MediaType, string(metadata), now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist artifact tombstone: %w", err))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "path": logicalPath})
}

func (h *Handler) validateArtifactLeaseHeaders(
	ctx context.Context,
	r *http.Request,
	workspaceID, logicalPath, ownerID string,
) error {
	if h.artifactLeases == nil {
		return artifactstore.ErrLeaseNotFound
	}
	token := strings.TrimSpace(r.Header.Get("X-Artifact-Lease-Token"))
	generation, err := strconv.ParseUint(strings.TrimSpace(r.Header.Get("X-Artifact-Lease-Generation")), 10, 64)
	if token == "" || err != nil || generation == 0 {
		return fmt.Errorf("%w: write lease token and generation are required", artifactstore.ErrLeaseHeld)
	}
	return h.artifactLeases.Validate(ctx, artifactstore.Lease{
		ScopeID: workspaceID,
		Path: logicalPath,
		OwnerID: ownerID,
		Token: token,
		Generation: generation,
	})
}

func (h *Handler) ensureArtifactRestoreUnlocked(
	ctx context.Context,
	workspaceID string,
	current, target map[string]artifactstore.Artifact,
) error {
	if h.artifactLeases == nil {
		return nil
	}
	paths := map[string]artifactstore.Artifact{}
	for path, artifact := range current {
		paths[path] = artifact
	}
	for path, artifact := range target {
		paths[path] = artifact
	}
	for artifactPath, artifact := range paths {
		before, beforeOK := current[artifactPath]
		after, afterOK := target[artifactPath]
		if beforeOK && afterOK && before.Descriptor.Digest == after.Descriptor.Digest {
			continue
		}
		policy := artifactstore.DefaultCollaborationPolicy(artifact)
		if !policy.LeaseRequired {
			continue
		}
		lease, err := h.artifactLeases.Get(ctx, workspaceID, artifactPath)
		if err == nil {
			return fmt.Errorf("%w: %s is leased by %s until %s",
				artifactstore.ErrLeaseHeld, artifactPath, lease.OwnerID, lease.ExpiresAt.Format(time.RFC3339))
		}
		if errors.Is(err, artifactstore.ErrLeaseNotFound) || errors.Is(err, artifactstore.ErrLeaseExpired) {
			continue
		}
		return err
	}
	return nil
}

func (h *Handler) RestoreWorkspaceArtifacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactManager == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("artifact storage is not configured"))
		return
	}
	workspaceID := chi.URLParam(r, "id")
	snapshotID := chi.URLParam(r, "snapshotID")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	var targetVersionRaw string
	err := h.db.QueryRowContext(ctx, `
		SELECT artifact_version_digest
		FROM workspace_snapshots
		WHERE id = $1 AND workspace_id = $2 AND artifact_version_digest IS NOT NULL
	`, snapshotID, workspaceID).Scan(&targetVersionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		respond.Error(w, http.StatusNotFound, errors.New("artifact snapshot not found"))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	targetVersionDigest, err := artifactstore.ParseDigest(targetVersionRaw)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("parse artifact version digest: %w", err))
		return
	}
	targetVersion, err := h.artifactManager.LoadVersion(ctx, targetVersionDigest)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("load target artifact version: %w", err))
		return
	}
	targetManifest, err := h.artifactManager.LoadManifest(ctx, targetVersion.Manifest)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("load target artifact manifest: %w", err))
		return
	}

	currentTree, err := h.currentWorkspaceArtifactTree(ctx, workspaceID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	targetTree := make(map[string]artifactstore.Artifact, len(targetManifest.Artifacts))
	for _, artifact := range targetManifest.Artifacts {
		targetTree[artifact.Path] = artifact
	}

	if err := h.ensureArtifactRestoreUnlocked(ctx, workspaceID, currentTree, targetTree); err != nil {
		respondArtifactLeaseError(w, err)
		return
	}

	var parents []artifactstore.Digest
	var currentVersion sql.NullString
	err = h.db.QueryRowContext(ctx, `
		SELECT artifact_version_digest
		FROM workspace_snapshots
		WHERE workspace_id = $1 AND artifact_version_digest IS NOT NULL
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&currentVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if currentVersion.Valid {
		if digest, parseErr := artifactstore.ParseDigest(currentVersion.String); parseErr == nil {
			parents = append(parents, digest)
		}
	}
	restoreVersion := artifactstore.Version{
		Schema:    artifactstore.VersionSchemaV1,
		Parents:   parents,
		Manifest:  targetVersion.Manifest,
		Author:    artifactstore.ActorIdentity{ID: user.UserID, Type: "human"},
		Message:   "restore artifacts from snapshot " + snapshotID,
		CreatedAt: time.Now().UTC(),
		Provenance: artifactstore.Provenance{WorkspaceID: workspaceID},
	}
	restoreDescriptor, err := h.artifactManager.PutVersion(ctx, restoreVersion)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("store restore artifact version: %w", err))
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()
	now := time.Now().UTC()

	for currentPath, currentArtifact := range currentTree {
		if _, keep := targetTree[currentPath]; keep {
			continue
		}
		metadata, _ := json.Marshal(map[string]any{
			"restored_from_snapshot": snapshotID,
			"previous_digest": currentArtifact.Descriptor.Digest.String(),
		})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO artifacts (
				id, organization_id, workspace_id, artifact_type, file_name, file_path,
				logical_path, kind, mime_type, size_bytes, is_tombstone, metadata, created_at
			) VALUES ($1, $2, $3, 'tombstone', $4, $5, $6, 'tombstone', $7, 0, true, $8, $9)
		`, uuid.NewString(), user.OrgID, workspaceID, path.Base(currentPath), currentPath,
			currentPath, currentArtifact.Descriptor.MediaType, string(metadata), now); err != nil {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist restore tombstone: %w", err))
			return
		}
	}

	for _, artifact := range targetManifest.Artifacts {
		payload, err := json.Marshal(artifact)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		metadata, _ := json.Marshal(map[string]any{
			"restored_from_snapshot": snapshotID,
			"artifact": artifact.Metadata,
		})
		var semanticAlgorithm, semanticHex any
		if artifact.SemanticDigest != nil {
			semanticAlgorithm = artifact.SemanticDigest.Algorithm
			semanticHex = artifact.SemanticDigest.Hex
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO artifacts (
				id, organization_id, workspace_id, artifact_type, file_name, file_path,
				logical_path, kind, mime_type, size_bytes, digest_algorithm, digest_hex,
				semantic_digest_algorithm, semantic_digest_hex, artifact_json,
				is_tombstone, metadata, created_at
			) VALUES (
				$1, $2, $3, 'workspace_file', $4, $5, $6, $7, $8, $9, $10, $11,
				$12, $13, $14, false, $15, $16
			)
		`, uuid.NewString(), user.OrgID, workspaceID, path.Base(artifact.Path), artifact.Path,
			artifact.Path, string(artifact.Kind), artifact.Descriptor.MediaType,
			artifact.Descriptor.Size, artifact.Descriptor.Digest.Algorithm, artifact.Descriptor.Digest.Hex,
			semanticAlgorithm, semanticHex, string(payload), string(metadata), now); err != nil {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist restored artifact: %w", err))
			return
		}
	}

	snapshotRowID := uuid.NewString()
	snapshotMetadata, _ := json.Marshal(map[string]string{"restored_from_snapshot": snapshotID})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_snapshots (
			id, workspace_id, artifact_manifest_digest, artifact_version_digest,
			description, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, snapshotRowID, workspaceID, targetVersion.Manifest.String(), restoreDescriptor.Digest.String(),
		"restore artifacts from snapshot "+snapshotID, string(snapshotMetadata), now); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist restore snapshot: %w", err))
		return
	}
	if err := tx.Commit(); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("commit artifact restore: %w", err))
		return
	}

	respond.JSON(w, http.StatusCreated, WorkspaceSnapshotResponse{
		ID:                     snapshotRowID,
		WorkspaceID:            workspaceID,
		ArtifactManifestDigest: targetVersion.Manifest.String(),
		ArtifactVersionDigest:  restoreDescriptor.Digest.String(),
		Description:            "restore artifacts from snapshot " + snapshotID,
		CreatedAt:              now,
	})
}

func (h *Handler) ListWorkspaceSnapshots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, git_commit, vcs_change_id, artifact_manifest_digest,
		       artifact_version_digest, description, created_at
		FROM workspace_snapshots
		WHERE workspace_id = $1
		ORDER BY created_at DESC
	`, workspaceID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	result := []WorkspaceSnapshotResponse{}
	for rows.Next() {
		var (
			item WorkspaceSnapshotResponse
			gitCommit, changeID, manifestDigest, versionDigest, description sql.NullString
		)
		item.WorkspaceID = workspaceID
		if err := rows.Scan(
			&item.ID, &gitCommit, &changeID, &manifestDigest,
			&versionDigest, &description, &item.CreatedAt,
		); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if gitCommit.Valid { item.GitCommit = gitCommit.String }
		if changeID.Valid { item.VCSChangeID = changeID.String }
		if manifestDigest.Valid { item.ArtifactManifestDigest = manifestDigest.String }
		if versionDigest.Valid { item.ArtifactVersionDigest = versionDigest.String }
		if description.Valid { item.Description = description.String }
		result = append(result, item)
	}
	respond.JSON(w, http.StatusOK, result)
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

