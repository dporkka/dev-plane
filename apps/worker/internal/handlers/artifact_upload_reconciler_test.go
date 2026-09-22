package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	_ "github.com/mattn/go-sqlite3"
)

func TestArtifactUploadReconcilerExpiresIncompleteMultipart(t *testing.T) {
	db := setupArtifactUploadReconcilerDB(t)
	defer db.Close()

	store := newReconcilerStore()
	manager, err := artifactstore.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 18, 0, 0, 0, time.UTC)
	insertReconcileUpload(t, db, reconcileUploadFixture{
		ID: "upload-expired", Status: "initiated",
		StagingKey: "uploads/ws/upload-expired", ProviderUploadID: "provider-1",
		DigestHex:    artifactstore.HashBytes([]byte("payload")).Hex,
		ExpectedSize: int64(len("payload")),
		ExpiresAt:    now.Add(-time.Minute), UpdatedAt: now.Add(-time.Hour),
	})

	reconciler := NewArtifactUploadReconciler(db, manager, slog.Default())
	reconciler.now = func() time.Time { return now }
	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	var status string
	var reconciledAt sql.NullTime
	var attempts int
	if err := db.QueryRow(`
		SELECT status, reconciled_at, reconciliation_attempts
		FROM artifact_uploads WHERE id = ?
	`, "upload-expired").Scan(&status, &reconciledAt, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "aborted" {
		t.Fatalf("status = %q, want aborted", status)
	}
	if !reconciledAt.Valid {
		t.Fatal("expected reconciled_at")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if store.abortCalls != 1 || store.deleteCalls != 1 {
		t.Fatalf("cleanup calls abort=%d delete=%d, want 1/1", store.abortCalls, store.deleteCalls)
	}
	metrics := reconciler.MetricsSnapshot()
	if metrics.Aborted != 1 || metrics.Claimed != 1 || metrics.Failures != 0 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestArtifactUploadReconcilerRecoversUploadedArtifact(t *testing.T) {
	db := setupArtifactUploadReconcilerDB(t)
	defer db.Close()

	store := newReconcilerStore()
	payload := []byte("large-media-payload")
	digest := artifactstore.HashBytes(payload)
	stagingKey := "uploads/ws/upload-recover"
	store.staging[stagingKey] = append([]byte(nil), payload...)

	manager, err := artifactstore.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 18, 0, 0, 0, time.UTC)
	insertReconcileUpload(t, db, reconcileUploadFixture{
		ID: "upload-recover", Status: "uploaded",
		StagingKey: stagingKey, ProviderUploadID: "provider-2",
		DigestHex: digest.Hex, ExpectedSize: int64(len(payload)),
		ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(-2 * time.Minute),
	})

	reconciler := NewArtifactUploadReconciler(db, manager, slog.Default())
	reconciler.now = func() time.Time { return now }
	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	var status string
	var artifactID sql.NullString
	if err := db.QueryRow(`
		SELECT status, artifact_id FROM artifact_uploads WHERE id = ?
	`, "upload-recover").Scan(&status, &artifactID); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || !artifactID.Valid || artifactID.String != "upload-recover" {
		t.Fatalf("upload status=%q artifact_id=%v", status, artifactID)
	}

	var storedDigest, metadata string
	if err := db.QueryRow(`
		SELECT digest_hex, metadata FROM artifacts WHERE id = ?
	`, "upload-recover").Scan(&storedDigest, &metadata); err != nil {
		t.Fatal(err)
	}
	if storedDigest != digest.Hex {
		t.Fatalf("artifact digest = %s, want %s", storedDigest, digest.Hex)
	}
	if !strings.Contains(metadata, "reconciled") {
		t.Fatalf("artifact metadata missing reconciliation marker: %s", metadata)
	}
	if _, ok := store.cas[digest.String()]; !ok {
		t.Fatal("verified payload was not promoted into CAS")
	}
	if _, ok := store.staging[stagingKey]; ok {
		t.Fatal("staging payload was not deleted")
	}
	metrics := reconciler.MetricsSnapshot()
	if metrics.Recovered != 1 || metrics.Completed != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestArtifactUploadReconcilerResetsStaleFinalizingSession(t *testing.T) {
	db := setupArtifactUploadReconcilerDB(t)
	defer db.Close()

	store := newReconcilerStore()
	manager, err := artifactstore.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 18, 0, 0, 0, time.UTC)
	insertReconcileUpload(t, db, reconcileUploadFixture{
		ID: "upload-finalizing", Status: "finalizing",
		StagingKey: "uploads/ws/missing", ProviderUploadID: "provider-3",
		DigestHex:    artifactstore.HashBytes([]byte("payload")).Hex,
		ExpectedSize: int64(len("payload")),
		ExpiresAt:    now.Add(time.Hour), UpdatedAt: now.Add(-10 * time.Minute),
	})

	reconciler := NewArtifactUploadReconciler(db, manager, slog.Default())
	reconciler.now = func() time.Time { return now }
	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	var status string
	var message sql.NullString
	if err := db.QueryRow(`
		SELECT status, error_message FROM artifact_uploads WHERE id = ?
	`, "upload-finalizing").Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	if status != "initiated" {
		t.Fatalf("status = %q, want initiated", status)
	}
	if !message.Valid || !strings.Contains(message.String, "stale finalization reset") {
		t.Fatalf("unexpected error message: %v", message)
	}
}

func TestArtifactUploadReconcilerSkipsActiveClaim(t *testing.T) {
	db := setupArtifactUploadReconcilerDB(t)
	defer db.Close()

	store := newReconcilerStore()
	manager, err := artifactstore.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 18, 0, 0, 0, time.UTC)
	insertReconcileUpload(t, db, reconcileUploadFixture{
		ID: "upload-claimed", Status: "initiated",
		StagingKey: "uploads/ws/claimed", ProviderUploadID: "provider-4",
		DigestHex:    artifactstore.HashBytes([]byte("payload")).Hex,
		ExpectedSize: int64(len("payload")),
		ExpiresAt:    now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	if _, err := db.Exec(`
		UPDATE artifact_uploads
		SET reconciliation_claim = ?, reconciliation_claim_expires_at = ?
		WHERE id = ?
	`, "other-worker", now.Add(time.Minute), "upload-claimed"); err != nil {
		t.Fatal(err)
	}

	reconciler := NewArtifactUploadReconciler(db, manager, slog.Default())
	reconciler.now = func() time.Time { return now }
	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.abortCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("actively claimed upload should not be touched: abort=%d delete=%d", store.abortCalls, store.deleteCalls)
	}
}

type reconcileUploadFixture struct {
	ID               string
	Status           string
	StagingKey       string
	ProviderUploadID string
	DigestHex        string
	ExpectedSize     int64
	ExpiresAt        time.Time
	UpdatedAt        time.Time
}

func setupArtifactUploadReconcilerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE artifact_uploads (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			logical_path TEXT NOT NULL,
			media_type TEXT NOT NULL,
			expected_digest_algorithm TEXT NOT NULL,
			expected_digest_hex TEXT NOT NULL,
			expected_size INTEGER NOT NULL,
			staging_key TEXT NOT NULL,
			provider_upload_id TEXT NOT NULL,
			part_size INTEGER NOT NULL,
			part_count INTEGER NOT NULL,
			status TEXT NOT NULL,
			initiated_by TEXT NOT NULL,
			artifact_id TEXT,
			error_message TEXT,
			expires_at DATETIME NOT NULL,
			reconciliation_claim TEXT,
			reconciliation_claim_expires_at DATETIME,
			reconciliation_attempts INTEGER NOT NULL DEFAULT 0,
			reconciled_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE artifacts (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_run_id TEXT,
			step_id TEXT,
			artifact_type TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			logical_path TEXT,
			kind TEXT,
			mime_type TEXT,
			size_bytes INTEGER,
			digest_algorithm TEXT,
			digest_hex TEXT,
			semantic_digest_algorithm TEXT,
			semantic_digest_hex TEXT,
			artifact_json TEXT,
			is_tombstone BOOLEAN NOT NULL DEFAULT false,
			metadata TEXT,
			created_at DATETIME NOT NULL
		);
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func insertReconcileUpload(t *testing.T, db *sql.DB, fixture reconcileUploadFixture) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO artifact_uploads (
			id, organization_id, workspace_id, logical_path, media_type,
			expected_digest_algorithm, expected_digest_hex, expected_size,
			staging_key, provider_upload_id, part_size, part_count, status,
			initiated_by, expires_at, created_at, updated_at
		) VALUES (?, 'org-1', 'workspace-1', 'media/file.bin', 'application/octet-stream',
			'sha256', ?, ?, ?, ?, 5242880, 1, ?, 'user-1', ?, ?, ?)
	`,
		fixture.ID,
		fixture.DigestHex,
		fixture.ExpectedSize,
		fixture.StagingKey,
		fixture.ProviderUploadID,
		fixture.Status,
		fixture.ExpiresAt,
		fixture.UpdatedAt.Add(-time.Minute),
		fixture.UpdatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
}

type reconcileStore struct {
	mu          sync.Mutex
	cas         map[string][]byte
	staging     map[string][]byte
	abortCalls  int
	deleteCalls int
}

func newReconcilerStore() *reconcileStore {
	return &reconcileStore{
		cas:     map[string][]byte{},
		staging: map[string][]byte{},
	}
}

func (s *reconcileStore) Put(_ context.Context, r io.Reader) (artifactstore.Descriptor, error) {
	payload, err := io.ReadAll(r)
	if err != nil {
		return artifactstore.Descriptor{}, err
	}
	digest := artifactstore.HashBytes(payload)
	s.mu.Lock()
	s.cas[digest.String()] = append([]byte(nil), payload...)
	s.mu.Unlock()
	return artifactstore.Descriptor{Digest: digest, Size: int64(len(payload))}, nil
}

func (s *reconcileStore) Open(_ context.Context, digest artifactstore.Digest) (io.ReadCloser, error) {
	s.mu.Lock()
	payload, ok := s.cas[digest.String()]
	s.mu.Unlock()
	if !ok {
		return nil, artifactstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func (s *reconcileStore) Has(_ context.Context, digest artifactstore.Digest) (bool, error) {
	s.mu.Lock()
	_, ok := s.cas[digest.String()]
	s.mu.Unlock()
	return ok, nil
}

func (s *reconcileStore) BeginMultipart(context.Context, string, string) (string, error) {
	return "upload", nil
}

func (s *reconcileStore) PresignUploadPart(context.Context, string, string, int, time.Duration) (artifactstore.PresignedPart, error) {
	return artifactstore.PresignedPart{}, nil
}

func (s *reconcileStore) CompleteMultipart(context.Context, string, string, []artifactstore.CompletedPart) error {
	return nil
}

func (s *reconcileStore) AbortMultipart(_ context.Context, _ string, _ string) error {
	s.mu.Lock()
	s.abortCalls++
	s.mu.Unlock()
	return nil
}

func (s *reconcileStore) VerifyObject(_ context.Context, key string, expected artifactstore.Descriptor) error {
	s.mu.Lock()
	payload, ok := s.staging[key]
	s.mu.Unlock()
	if !ok {
		return artifactstore.ErrNotFound
	}
	digest := artifactstore.HashBytes(payload)
	if digest != expected.Digest || int64(len(payload)) != expected.Size {
		return errors.New("artifact integrity mismatch")
	}
	return nil
}

func (s *reconcileStore) PromoteToCAS(_ context.Context, key string, expected artifactstore.Descriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, ok := s.staging[key]
	if !ok {
		return artifactstore.ErrNotFound
	}
	s.cas[expected.Digest.String()] = append([]byte(nil), payload...)
	return nil
}

func (s *reconcileStore) DeleteObject(_ context.Context, key string) error {
	s.mu.Lock()
	s.deleteCalls++
	delete(s.staging, key)
	s.mu.Unlock()
	return nil
}