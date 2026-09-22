package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync/atomic"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/google/uuid"
)

const (
	defaultArtifactUploadReconcileBatch    = 50
	defaultArtifactUploadClaimTTL          = 2 * time.Minute
	defaultArtifactUploadStaleFinalization = 5 * time.Minute
	defaultArtifactUploadRetryDelay        = time.Minute
)

type ArtifactUploadReconcileMetrics struct {
	Runs            int64 `json:"runs"`
	Claimed         int64 `json:"claimed"`
	Recovered       int64 `json:"recovered"`
	Completed       int64 `json:"completed"`
	Aborted         int64 `json:"aborted"`
	Failures        int64 `json:"failures"`
	LastRunUnix     int64 `json:"last_run_unix,omitempty"`
	LastSuccessUnix int64 `json:"last_success_unix,omitempty"`
	LastFailureUnix int64 `json:"last_failure_unix,omitempty"`
}

type artifactUploadMetricState struct {
	runs            atomic.Int64
	claimed         atomic.Int64
	recovered       atomic.Int64
	completed       atomic.Int64
	aborted         atomic.Int64
	failures        atomic.Int64
	lastRunUnix     atomic.Int64
	lastSuccessUnix atomic.Int64
	lastFailureUnix atomic.Int64
}

type ArtifactUploadReconciler struct {
	db       *sql.DB
	manager  *artifactstore.Manager
	registry *artifactstore.AdapterRegistry
	logger   *slog.Logger
	now      func() time.Time
	batch    int
	claimTTL time.Duration
	metrics  artifactUploadMetricState
}

type reconciledArtifactUpload struct {
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
	Status           string
	InitiatedBy      string
	ExpiresAt        time.Time
	UpdatedAt        time.Time
}

func NewArtifactUploadReconciler(db *sql.DB, manager *artifactstore.Manager, logger *slog.Logger) *ArtifactUploadReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ArtifactUploadReconciler{
		db:       db,
		manager:  manager,
		registry: artifactstore.NewDefaultAdapterRegistry(),
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
		batch:    defaultArtifactUploadReconcileBatch,
		claimTTL: defaultArtifactUploadClaimTTL,
	}
}

func (r *ArtifactUploadReconciler) MetricsSnapshot() ArtifactUploadReconcileMetrics {
	if r == nil {
		return ArtifactUploadReconcileMetrics{}
	}
	return ArtifactUploadReconcileMetrics{
		Runs:            r.metrics.runs.Load(),
		Claimed:         r.metrics.claimed.Load(),
		Recovered:       r.metrics.recovered.Load(),
		Completed:       r.metrics.completed.Load(),
		Aborted:         r.metrics.aborted.Load(),
		Failures:        r.metrics.failures.Load(),
		LastRunUnix:     r.metrics.lastRunUnix.Load(),
		LastSuccessUnix: r.metrics.lastSuccessUnix.Load(),
		LastFailureUnix: r.metrics.lastFailureUnix.Load(),
	}
}

func (r *ArtifactUploadReconciler) Reconcile(ctx context.Context) error {
	if r == nil || r.db == nil || r.manager == nil || !r.manager.SupportsDirectMultipart() {
		return nil
	}
	now := r.now()
	r.metrics.runs.Add(1)
	r.metrics.lastRunUnix.Store(now.Unix())

	candidates, err := r.listCandidates(ctx, now)
	if err != nil {
		r.recordFailure(now)
		return err
	}

	var firstErr error
	for _, upload := range candidates {
		claim := uuid.NewString()
		claimed, err := r.claim(ctx, upload, claim, now)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			r.recordFailure(now)
			continue
		}
		if !claimed {
			continue
		}
		r.metrics.claimed.Add(1)

		processCtx, cancelProcess := context.WithCancel(ctx)
		heartbeatErr := make(chan error, 1)
		heartbeatDone := make(chan struct{})
		go r.heartbeatClaim(processCtx, cancelProcess, upload.ID, claim, heartbeatErr, heartbeatDone)

		reconcileErr := r.reconcileClaimed(processCtx, upload, claim, now)
		cancelProcess()
		<-heartbeatDone
		select {
		case err := <-heartbeatErr:
			if reconcileErr == nil {
				reconcileErr = err
			}
		default:
		}

		if reconcileErr != nil {
			r.logger.Warn("artifact upload reconciliation failed",
				"upload_id", upload.ID,
				"status", upload.Status,
				"error", reconcileErr,
			)
			r.metrics.failures.Add(1)
			r.metrics.lastFailureUnix.Store(now.Unix())
			if firstErr == nil {
				firstErr = reconcileErr
			}
			_ = r.releaseClaim(context.Background(), upload.ID, claim, upload.Status, reconcileErr.Error(), r.now())
		}
	}
	if firstErr == nil {
		r.metrics.lastSuccessUnix.Store(now.Unix())
	}
	return firstErr
}

func (r *ArtifactUploadReconciler) listCandidates(ctx context.Context, now time.Time) ([]reconciledArtifactUpload, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, organization_id, workspace_id, logical_path, media_type,
		       expected_digest_algorithm, expected_digest_hex, expected_size,
		       staging_key, provider_upload_id, status, initiated_by,
		       expires_at, updated_at
		FROM artifact_uploads
		WHERE (
			reconciliation_claim IS NULL
			OR reconciliation_claim_expires_at IS NULL
			OR reconciliation_claim_expires_at <= $1
		)
		AND (
			(status = 'initiated' AND expires_at <= $1)
			OR (status = 'finalizing' AND updated_at <= $2)
			OR (status IN ('uploaded', 'verified', 'cleanup_pending') AND updated_at <= $3)
		)
		ORDER BY updated_at ASC, id ASC
		LIMIT $4
	`,
		now,
		now.Add(-defaultArtifactUploadStaleFinalization),
		now.Add(-defaultArtifactUploadRetryDelay),
		r.batch,
	)
	if err != nil {
		return nil, fmt.Errorf("list artifact uploads for reconciliation: %w", err)
	}
	defer rows.Close()

	var uploads []reconciledArtifactUpload
	for rows.Next() {
		var upload reconciledArtifactUpload
		if err := rows.Scan(
			&upload.ID,
			&upload.OrganizationID,
			&upload.WorkspaceID,
			&upload.LogicalPath,
			&upload.MediaType,
			&upload.DigestAlgorithm,
			&upload.DigestHex,
			&upload.ExpectedSize,
			&upload.StagingKey,
			&upload.ProviderUploadID,
			&upload.Status,
			&upload.InitiatedBy,
			&upload.ExpiresAt,
			&upload.UpdatedAt,
		); err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return uploads, nil
}

func (r *ArtifactUploadReconciler) claim(ctx context.Context, upload reconciledArtifactUpload, claim string, now time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE artifact_uploads
		SET reconciliation_claim = $1,
		    reconciliation_claim_expires_at = $2,
		    reconciliation_attempts = reconciliation_attempts + 1
		WHERE id = $3
		  AND status = $4
		  AND (
			reconciliation_claim IS NULL
			OR reconciliation_claim_expires_at IS NULL
			OR reconciliation_claim_expires_at <= $5
		  )
	`, claim, now.Add(r.claimTTL), upload.ID, upload.Status, now)
	if err != nil {
		return false, fmt.Errorf("claim artifact upload %s: %w", upload.ID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (r *ArtifactUploadReconciler) heartbeatClaim(
	ctx context.Context,
	cancel context.CancelFunc,
	uploadID, claim string,
	errCh chan<- error,
	done chan<- struct{},
) {
	defer close(done)
	interval := r.claimTTL / 3
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, err := r.renewClaim(ctx, uploadID, claim, r.now())
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}
			if !renewed {
				select {
				case errCh <- fmt.Errorf("artifact upload reconciliation claim %s was lost", uploadID):
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (r *ArtifactUploadReconciler) renewClaim(
	ctx context.Context,
	uploadID, claim string,
	now time.Time,
) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE artifact_uploads
		SET reconciliation_claim_expires_at = $1
		WHERE id = $2 AND reconciliation_claim = $3
	`, now.Add(r.claimTTL), uploadID, claim)
	if err != nil {
		return false, fmt.Errorf("renew artifact upload reconciliation claim %s: %w", uploadID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (r *ArtifactUploadReconciler) reconcileClaimed(ctx context.Context, upload reconciledArtifactUpload, claim string, now time.Time) error {
	switch upload.Status {
	case "initiated":
		return r.cleanupExpired(ctx, upload, claim, "expired incomplete multipart upload", now)
	case "cleanup_pending":
		return r.cleanupExpired(ctx, upload, claim, "pending multipart cleanup completed", now)
	case "finalizing":
		if err := r.manager.VerifyAndPromoteMultipart(ctx, upload.StagingKey, upload.descriptor()); err == nil {
			r.metrics.recovered.Add(1)
			return r.finalizeVerified(ctx, upload, claim, now)
		} else if !upload.ExpiresAt.After(now) {
			return r.cleanupExpired(ctx, upload, claim, "expired stale finalization: "+err.Error(), now)
		} else {
			return r.transition(ctx, upload.ID, claim, "initiated", "stale finalization reset: "+err.Error(), now, false)
		}
	case "uploaded":
		if err := r.manager.VerifyAndPromoteMultipart(ctx, upload.StagingKey, upload.descriptor()); err == nil {
			r.metrics.recovered.Add(1)
			return r.finalizeVerified(ctx, upload, claim, now)
		} else if !upload.ExpiresAt.After(now) {
			return r.cleanupExpired(ctx, upload, claim, "expired uploaded object: "+err.Error(), now)
		} else {
			return r.transition(ctx, upload.ID, claim, "uploaded", err.Error(), now, false)
		}
	case "verified":
		return r.finalizeVerified(ctx, upload, claim, now)
	default:
		return r.releaseClaim(ctx, upload.ID, claim, upload.Status, "", now)
	}
}

func (u reconciledArtifactUpload) descriptor() artifactstore.Descriptor {
	return artifactstore.Descriptor{
		Digest: artifactstore.Digest{
			Algorithm: u.DigestAlgorithm,
			Hex:       u.DigestHex,
		},
		Size:      u.ExpectedSize,
		MediaType: u.MediaType,
	}
}

func (r *ArtifactUploadReconciler) cleanupExpired(ctx context.Context, upload reconciledArtifactUpload, claim, reason string, now time.Time) error {
	var cleanupErrs []error
	if strings.TrimSpace(upload.ProviderUploadID) != "" {
		if err := r.manager.AbortMultipart(ctx, upload.StagingKey, upload.ProviderUploadID); err != nil &&
			!errors.Is(err, artifactstore.ErrNotFound) {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	if err := r.manager.DeleteDirectStaging(ctx, upload.StagingKey); err != nil &&
		!errors.Is(err, artifactstore.ErrNotFound) {
		cleanupErrs = append(cleanupErrs, err)
	}
	if len(cleanupErrs) > 0 {
		message := reason + ": " + errors.Join(cleanupErrs...).Error()
		return r.transition(ctx, upload.ID, claim, "cleanup_pending", message, now, false)
	}
	r.metrics.aborted.Add(1)
	return r.transition(ctx, upload.ID, claim, "aborted", reason, now, true)
}

func (r *ArtifactUploadReconciler) finalizeVerified(ctx context.Context, upload reconciledArtifactUpload, claim string, now time.Time) error {
	var existing string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM artifacts WHERE id = $1`, upload.ID).Scan(&existing)
	if err == nil {
		r.metrics.completed.Add(1)
		return r.completeUpload(ctx, upload.ID, claim, existing, now)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check reconciled artifact %s: %w", upload.ID, err)
	}

	artifact := artifactstore.Artifact{
		Path:       upload.LogicalPath,
		Kind:       artifactstore.KindBinary,
		Descriptor: upload.descriptor(),
		Metadata: map[string]string{
			"workspace_id":     upload.WorkspaceID,
			"uploaded_by":      upload.InitiatedBy,
			"direct_upload_id": upload.ID,
			"reconciled":       "true",
		},
	}
	var analysis artifactstore.AdapterAnalysis
	if reconcileShouldAnalyzeArtifact(upload.LogicalPath, upload.MediaType) {
		analyzed, result, analysisErr := r.manager.AnalyzeArtifact(ctx, r.registry, artifact, artifactstore.AnalyzeOptions{})
		if analysisErr != nil {
			artifact.Metadata["analysis_error"] = analysisErr.Error()
		} else {
			artifact = analyzed
			analysis = result
		}
	}

	payload, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("marshal reconciled artifact: %w", err)
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
		return fmt.Errorf("marshal reconciled artifact metadata: %w", err)
	}

	var semanticAlgorithm, semanticHex any
	if artifact.SemanticDigest != nil {
		semanticAlgorithm = artifact.SemanticDigest.Algorithm
		semanticHex = artifact.SemanticDigest.Hex
	}
	_, err = r.db.ExecContext(ctx, `
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
		upload.ID,
		upload.OrganizationID,
		upload.WorkspaceID,
		path.Base(artifact.Path),
		artifact.Path,
		artifact.Path,
		string(artifact.Kind),
		artifact.Descriptor.MediaType,
		artifact.Descriptor.Size,
		artifact.Descriptor.Digest.Algorithm,
		artifact.Descriptor.Digest.Hex,
		semanticAlgorithm,
		semanticHex,
		string(payload),
		string(metadata),
		now,
	)
	if err != nil {
		if lookupErr := r.db.QueryRowContext(ctx, `SELECT id FROM artifacts WHERE id = $1`, upload.ID).Scan(&existing); lookupErr != nil {
			return fmt.Errorf("persist reconciled artifact %s: %w", upload.ID, err)
		}
	}
	r.metrics.completed.Add(1)
	return r.completeUpload(ctx, upload.ID, claim, upload.ID, now)
}

func (r *ArtifactUploadReconciler) completeUpload(ctx context.Context, uploadID, claim, artifactID string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE artifact_uploads
		SET status = 'completed',
		    artifact_id = $1,
		    error_message = NULL,
		    reconciled_at = $2,
		    reconciliation_claim = NULL,
		    reconciliation_claim_expires_at = NULL,
		    updated_at = $2
		WHERE id = $3 AND reconciliation_claim = $4
	`, artifactID, now, uploadID, claim)
	if err != nil {
		return fmt.Errorf("complete reconciled artifact upload %s: %w", uploadID, err)
	}
	return nil
}

func (r *ArtifactUploadReconciler) transition(ctx context.Context, uploadID, claim, status, message string, now time.Time, reconciled bool) error {
	var reconciledAt any
	if reconciled {
		reconciledAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE artifact_uploads
		SET status = $1,
		    error_message = $2,
		    reconciled_at = $3,
		    reconciliation_claim = NULL,
		    reconciliation_claim_expires_at = NULL,
		    updated_at = $4
		WHERE id = $5 AND reconciliation_claim = $6
	`, status, nullReconcileMessage(message), reconciledAt, now, uploadID, claim)
	if err != nil {
		return fmt.Errorf("transition reconciled upload %s to %s: %w", uploadID, status, err)
	}
	return nil
}

func (r *ArtifactUploadReconciler) releaseClaim(ctx context.Context, uploadID, claim, status, message string, now time.Time) error {
	return r.transition(ctx, uploadID, claim, status, message, now, false)
}

func nullReconcileMessage(message string) any {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	return message
}

func reconcileShouldAnalyzeArtifact(logicalPath, mediaType string) bool {
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

func (r *ArtifactUploadReconciler) recordFailure(now time.Time) {
	r.metrics.failures.Add(1)
	r.metrics.lastFailureUnix.Store(now.Unix())
}
