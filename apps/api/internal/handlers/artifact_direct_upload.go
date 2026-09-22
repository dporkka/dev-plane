package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	maxDirectArtifactUploadBytes int64 = 5 << 40
	directUploadTTL                    = 24 * time.Hour
	initialPresignBatch                = 32
	maxPresignBatch                    = 100
)

type BeginArtifactUploadRequest struct {
	Path          string `json:"path"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	ContentType   string `json:"content_type,omitempty"`
	PartSizeBytes int64  `json:"part_size_bytes,omitempty"`
}

type BeginArtifactUploadResponse struct {
	ID          string                        `json:"id"`
	WorkspaceID string                        `json:"workspace_id"`
	Path        string                        `json:"path"`
	Digest      artifactstore.Digest          `json:"digest"`
	SizeBytes   int64                         `json:"size_bytes"`
	ContentType string                        `json:"content_type"`
	PartSize    int64                         `json:"part_size_bytes"`
	PartCount   int                           `json:"part_count"`
	Status      string                        `json:"status"`
	ExpiresAt   time.Time                     `json:"expires_at"`
	Parts       []artifactstore.PresignedPart `json:"parts,omitempty"`
}

type PresignArtifactPartsRequest struct {
	StartPart int `json:"start_part"`
	Count     int `json:"count"`
}

type CompleteArtifactUploadRequest struct {
	Parts []artifactstore.CompletedPart `json:"parts"`
}

type directUploadRecord struct {
	ID               string
	OrganizationID   string
	WorkspaceID      string
	LogicalPath      string
	MediaType        string
	DigestAlgorithm  string
	DigestHex        string
	ExpectedSize     int64
	StagingKey       string
	ProviderUploadID string
	PartSize         int64
	PartCount        int
	Status           string
	InitiatedBy      string
	ArtifactID       sql.NullString
	ErrorMessage     sql.NullString
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (u directUploadRecord) descriptor() artifactstore.Descriptor {
	return artifactstore.Descriptor{
		Digest: artifactstore.Digest{Algorithm: u.DigestAlgorithm, Hex: u.DigestHex},
		Size: u.ExpectedSize,
		MediaType: u.MediaType,
	}
}

func (h *Handler) BeginArtifactUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.artifactManager == nil || !h.artifactManager.SupportsDirectMultipart() {
		respond.Error(w, http.StatusServiceUnavailable, artifactstore.ErrDirectUploadUnsupported)
		return
	}
	workspaceID := chi.URLParam(r, "id")
	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	var req BeginArtifactUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	logicalPath, err := artifactstore.NormalizeArtifactPath(req.Path)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.SizeBytes < 0 || req.SizeBytes > maxDirectArtifactUploadBytes {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("size_bytes must be between 0 and %d", maxDirectArtifactUploadBytes))
		return
	}
	digest, err := artifactstore.ParseDigest(artifactstore.AlgorithmSHA256 + ":" + strings.ToLower(strings.TrimSpace(req.SHA256)))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if err := h.validateArtifactWriteLease(ctx, r, user, workspaceID, logicalPath); err != nil {
		respondArtifactLeaseError(w, err)
		return
	}
	partSize, partCount, err := artifactstore.MultipartPartSize(req.SizeBytes, req.PartSizeBytes)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	mediaType := strings.TrimSpace(req.ContentType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	id := uuid.NewString()
	stagingKey := path.Join("uploads", user.OrgID, workspaceID, id)
	providerUploadID, err := h.artifactManager.BeginMultipart(ctx, stagingKey, mediaType)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, fmt.Errorf("initiate multipart upload: %w", err))
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(directUploadTTL)
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO artifact_uploads (
			id, organization_id, workspace_id, logical_path, media_type,
			expected_digest_algorithm, expected_digest_hex, expected_size,
			staging_key, provider_upload_id, part_size, part_count, status,
			initiated_by, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, 'initiated', $13, $14, $15, $15
		)
	`, id, user.OrgID, workspaceID, logicalPath, mediaType,
		digest.Algorithm, digest.Hex, req.SizeBytes, stagingKey, providerUploadID,
		partSize, partCount, user.UserID, expiresAt, now)
	if err != nil {
		_ = h.artifactManager.AbortMultipart(context.Background(), stagingKey, providerUploadID)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist artifact upload session: %w", err))
		return
	}

	batchCount := partCount
	if batchCount > initialPresignBatch {
		batchCount = initialPresignBatch
	}
	parts, err := h.artifactManager.PresignUploadParts(ctx, stagingKey, providerUploadID, 1, batchCount, artifactstore.DefaultPresignTTL)
	if err != nil {
		_ = h.artifactManager.AbortMultipart(context.Background(), stagingKey, providerUploadID)
		_, _ = h.db.ExecContext(context.Background(),
			`UPDATE artifact_uploads SET status = 'aborted', error_message = $1, updated_at = $2 WHERE id = $3`,
			err.Error(), time.Now().UTC(), id)
		respond.Error(w, http.StatusBadGateway, fmt.Errorf("presign upload parts: %w", err))
		return
	}

	respond.JSON(w, http.StatusCreated, BeginArtifactUploadResponse{
		ID: id, WorkspaceID: workspaceID, Path: logicalPath, Digest: digest,
		SizeBytes: req.SizeBytes, ContentType: mediaType, PartSize: partSize,
		PartCount: partCount, Status: "initiated", ExpiresAt: expiresAt, Parts: parts,
	})
}

func (h *Handler) PresignArtifactUploadParts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	upload, err := h.loadDirectUpload(ctx, chi.URLParam(r, "uploadID"), chi.URLParam(r, "id"), user)
	if err != nil {
		h.respondDirectUploadLookupError(w, err)
		return
	}
	if upload.Status != "initiated" {
		respond.Error(w, http.StatusConflict, fmt.Errorf("artifact upload is %s", upload.Status))
		return
	}
	if time.Now().UTC().After(upload.ExpiresAt) {
		respond.Error(w, http.StatusGone, errors.New("artifact upload session expired"))
		return
	}
	var req PresignArtifactPartsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.StartPart < 1 || req.StartPart > upload.PartCount {
		respond.Error(w, http.StatusBadRequest, errors.New("start_part is outside the upload part range"))
		return
	}
	if req.Count <= 0 {
		req.Count = initialPresignBatch
	}
	if req.Count > maxPresignBatch {
		req.Count = maxPresignBatch
	}
	if req.StartPart+req.Count-1 > upload.PartCount {
		req.Count = upload.PartCount - req.StartPart + 1
	}
	parts, err := h.artifactManager.PresignUploadParts(
		ctx, upload.StagingKey, upload.ProviderUploadID,
		req.StartPart, req.Count, artifactstore.DefaultPresignTTL,
	)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"upload_id": upload.ID, "start_part": req.StartPart, "count": len(parts), "parts": parts,
	})
}

func (h *Handler) CompleteArtifactUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	upload, err := h.loadDirectUpload(ctx, chi.URLParam(r, "uploadID"), chi.URLParam(r, "id"), user)
	if err != nil {
		h.respondDirectUploadLookupError(w, err)
		return
	}
	if upload.Status == "completed" && upload.ArtifactID.Valid {
		h.respondCompletedDirectUpload(w, r, upload.ArtifactID.String)
		return
	}
	if upload.Status == "aborted" {
		respond.Error(w, http.StatusConflict, errors.New("artifact upload was aborted"))
		return
	}
	if time.Now().UTC().After(upload.ExpiresAt) && upload.Status == "initiated" {
		respond.Error(w, http.StatusGone, errors.New("artifact upload session expired"))
		return
	}
	if err := h.validateArtifactWriteLease(ctx, r, user, upload.WorkspaceID, upload.LogicalPath); err != nil {
		respondArtifactLeaseError(w, err)
		return
	}

	var req CompleteArtifactUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if upload.Status == "finalizing" && time.Since(upload.UpdatedAt) < 5*time.Minute {
		respond.Error(w, http.StatusConflict, errors.New("artifact upload completion is already in progress"))
		return
	}

	if upload.Status == "initiated" || upload.Status == "finalizing" {
		if len(req.Parts) != upload.PartCount {
			respond.Error(w, http.StatusBadRequest, fmt.Errorf("expected %d completed parts, got %d", upload.PartCount, len(req.Parts)))
			return
		}
		for i, part := range req.Parts {
			if part.PartNumber != i+1 || strings.TrimSpace(part.ETag) == "" {
				respond.Error(w, http.StatusBadRequest, errors.New("parts must be consecutive from 1 with non-empty ETags"))
				return
			}
		}
		claimTime := time.Now().UTC()
		result, err := h.db.ExecContext(ctx, `
			UPDATE artifact_uploads
			SET status = 'finalizing', error_message = NULL, updated_at = $1
			WHERE id = $2
			  AND (
				status = 'initiated'
				OR (status = 'finalizing' AND updated_at < $3)
			  )
		`, claimTime, upload.ID, claimTime.Add(-5*time.Minute))
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		rows, err := result.RowsAffected()
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if rows == 0 {
			respond.Error(w, http.StatusConflict, errors.New("artifact upload completion is already in progress"))
			return
		}

		completeErr := h.artifactManager.CompleteMultipart(ctx, upload.StagingKey, upload.ProviderUploadID, req.Parts)
		if completeErr != nil {
			// A lost provider response is ambiguous: if the staged object now
			// verifies, treat completion as successful; otherwise keep the
			// finalizing state so the exact request can be retried.
			if verifyErr := h.artifactManager.VerifyAndPromoteMultipart(ctx, upload.StagingKey, upload.descriptor()); verifyErr != nil {
				_, _ = h.db.ExecContext(context.Background(), `
					UPDATE artifact_uploads
					SET status = 'initiated', error_message = $1, updated_at = $2
					WHERE id = $3
				`, completeErr.Error(), time.Now().UTC(), upload.ID)
				respond.Error(w, http.StatusBadGateway, fmt.Errorf("complete multipart upload: %w", completeErr))
				return
			}
			upload.Status = "verified"
		} else {
			_, _ = h.db.ExecContext(ctx,
				`UPDATE artifact_uploads SET status = 'uploaded', updated_at = $1 WHERE id = $2`,
				time.Now().UTC(), upload.ID)
			upload.Status = "uploaded"
		}
	}

	if upload.Status == "uploaded" {
		if err := h.artifactManager.VerifyAndPromoteMultipart(ctx, upload.StagingKey, upload.descriptor()); err != nil {
			_, _ = h.db.ExecContext(context.Background(),
				`UPDATE artifact_uploads SET error_message = $1, updated_at = $2 WHERE id = $3`,
				err.Error(), time.Now().UTC(), upload.ID)
			respond.Error(w, http.StatusBadRequest, fmt.Errorf("verify uploaded artifact: %w", err))
			return
		}
		upload.Status = "verified"
	}
	if upload.Status == "verified" {
		_, err = h.db.ExecContext(ctx, `
			UPDATE artifact_uploads
			SET status = 'verified', error_message = NULL, updated_at = $1
			WHERE id = $2
		`, time.Now().UTC(), upload.ID)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
	}

	record, err := h.persistVerifiedDirectUpload(ctx, user, upload)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusCreated, record)
}

func (h *Handler) AbortArtifactUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	upload, err := h.loadDirectUpload(ctx, chi.URLParam(r, "uploadID"), chi.URLParam(r, "id"), user)
	if err != nil {
		h.respondDirectUploadLookupError(w, err)
		return
	}
	if upload.Status == "completed" {
		respond.Error(w, http.StatusConflict, errors.New("completed artifact upload cannot be aborted"))
		return
	}
	if upload.Status == "initiated" || upload.Status == "finalizing" {
		if err := h.artifactManager.AbortMultipart(ctx, upload.StagingKey, upload.ProviderUploadID); err != nil {
			h.logger.Warn("failed to abort provider multipart upload", "upload_id", upload.ID, "error", err)
		}
	} else {
		if err := h.artifactManager.DeleteDirectStaging(ctx, upload.StagingKey); err != nil {
			h.logger.Warn("failed to delete direct-upload staging object", "upload_id", upload.ID, "error", err)
		}
	}
	_, err = h.db.ExecContext(ctx, `
		UPDATE artifact_uploads SET status = 'aborted', updated_at = $1
		WHERE id = $2 AND status <> 'completed'
	`, time.Now().UTC(), upload.ID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "aborted", "id": upload.ID})
}

func (h *Handler) persistVerifiedDirectUpload(ctx context.Context, user *auth.Claims, upload directUploadRecord) (ArtifactMetadataResponse, error) {
	if record, artifact, err := h.loadCASArtifact(ctx, upload.ID); err == nil {
		_, _ = h.db.ExecContext(ctx, `
			UPDATE artifact_uploads SET status = 'completed', artifact_id = $1, updated_at = $2
			WHERE id = $1
		`, upload.ID, time.Now().UTC())
		workspaceID := upload.WorkspaceID
		return artifactResponse(record.ID, record.OrganizationID, &workspaceID, artifact, record.CreatedAt), nil
	}

	artifact := artifactstore.Artifact{
		Path: upload.LogicalPath,
		Kind: artifactstore.KindBinary,
		Descriptor: upload.descriptor(),
		Metadata: map[string]string{
			"workspace_id": upload.WorkspaceID,
			"uploaded_by": user.UserID,
			"direct_upload_id": upload.ID,
		},
	}
	var analysis artifactstore.AdapterAnalysis
	if shouldAnalyzeArtifact(upload.LogicalPath, upload.MediaType) && h.artifactRegistry != nil {
		analyzed, result, analysisErr := h.artifactManager.AnalyzeArtifact(ctx, h.artifactRegistry, artifact, artifactstore.AnalyzeOptions{})
		if analysisErr != nil {
			artifact.Metadata["analysis_error"] = analysisErr.Error()
		} else {
			artifact = analyzed
			analysis = result
		}
	}
	record, err := h.persistArtifactWithID(ctx, upload.ID, user.OrgID, upload.WorkspaceID, artifact, analysis)
	if err != nil {
		if existing, existingArtifact, loadErr := h.loadCASArtifact(ctx, upload.ID); loadErr == nil {
			workspaceID := upload.WorkspaceID
			record = artifactResponse(existing.ID, existing.OrganizationID, &workspaceID, existingArtifact, existing.CreatedAt)
		} else {
			return ArtifactMetadataResponse{}, err
		}
	}
	_, err = h.db.ExecContext(ctx, `
		UPDATE artifact_uploads
		SET status = 'completed', artifact_id = $1, error_message = NULL, updated_at = $2
		WHERE id = $1
	`, upload.ID, time.Now().UTC())
	if err != nil {
		return ArtifactMetadataResponse{}, fmt.Errorf("mark direct upload completed: %w", err)
	}
	return record, nil
}

func (h *Handler) loadDirectUpload(ctx context.Context, uploadID, workspaceID string, user *auth.Claims) (directUploadRecord, error) {
	var upload directUploadRecord
	err := h.db.QueryRowContext(ctx, `
		SELECT id, organization_id, workspace_id, logical_path, media_type,
		       expected_digest_algorithm, expected_digest_hex, expected_size,
		       staging_key, provider_upload_id, part_size, part_count, status,
		       initiated_by, artifact_id, error_message, expires_at, created_at, updated_at
		FROM artifact_uploads
		WHERE id = $1 AND workspace_id = $2 AND organization_id = $3 AND initiated_by = $4
	`, uploadID, workspaceID, user.OrgID, user.UserID).Scan(
		&upload.ID, &upload.OrganizationID, &upload.WorkspaceID,
		&upload.LogicalPath, &upload.MediaType,
		&upload.DigestAlgorithm, &upload.DigestHex, &upload.ExpectedSize,
		&upload.StagingKey, &upload.ProviderUploadID,
		&upload.PartSize, &upload.PartCount, &upload.Status,
		&upload.InitiatedBy, &upload.ArtifactID, &upload.ErrorMessage,
		&upload.ExpiresAt, &upload.CreatedAt, &upload.UpdatedAt,
	)
	return upload, err
}

func (h *Handler) respondDirectUploadLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		respond.Error(w, http.StatusNotFound, errors.New("artifact upload not found"))
		return
	}
	respond.Error(w, http.StatusInternalServerError, err)
}

func (h *Handler) respondCompletedDirectUpload(w http.ResponseWriter, r *http.Request, artifactID string) {
	record, artifact, err := h.loadCASArtifact(r.Context(), artifactID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	var workspaceID *string
	if record.WorkspaceID.Valid {
		workspaceID = &record.WorkspaceID.String
	}
	respond.JSON(w, http.StatusOK, artifactResponse(record.ID, record.OrganizationID, workspaceID, artifact, record.CreatedAt))
}

func (h *Handler) validateArtifactWriteLease(ctx context.Context, r *http.Request, user *auth.Claims, workspaceID, logicalPath string) error {
	existing, err := h.latestWorkspaceArtifact(ctx, workspaceID, logicalPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !artifactstore.DefaultCollaborationPolicy(existing).LeaseRequired {
		return nil
	}
	if h.artifactLeases == nil {
		return fmt.Errorf("%w: artifact update requires a write lease", artifactstore.ErrLeaseHeld)
	}
	token := strings.TrimSpace(r.Header.Get("X-Artifact-Lease-Token"))
	generation, parseErr := strconv.ParseUint(strings.TrimSpace(r.Header.Get("X-Artifact-Lease-Generation")), 10, 64)
	if token == "" || parseErr != nil || generation == 0 {
		return fmt.Errorf("%w: artifact update requires X-Artifact-Lease-Token and X-Artifact-Lease-Generation", artifactstore.ErrLeaseHeld)
	}
	return h.artifactLeases.Validate(ctx, artifactstore.Lease{
		ScopeID: workspaceID, Path: logicalPath, OwnerID: user.UserID,
		Token: token, Generation: generation,
	})
}
