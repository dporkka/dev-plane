// Package secrets stores secret values encrypted at rest and exposes raw values
// only through explicit audited calls.
package secrets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/crypto"
	"github.com/ai-dev-control-plane/models"
)

const (
	defaultKeyPathPrefix = "db://secrets/"
)

var (
	ErrSecretNotFound = errors.New("secret not found")
	ErrSecretKey      = errors.New("secret encryption key error")
)

// ParseKeyring parses comma-separated key specs in the form
// key-id:base64-encoded-32-byte-key. The first key is used for new writes.
func ParseKeyring(raw string) (*crypto.Keyring, error) {
	return crypto.ParseKeyring(raw)
}

// NewSingleKeyring creates a keyring with a single key.
func NewSingleKeyring(id string, key []byte) (*crypto.Keyring, error) {
	return crypto.NewSingleKeyring(id, key)
}

type Manager struct {
	db     *sql.DB
	keys   *crypto.Keyring
	audit  *audit.Logger
	logger *slog.Logger
}

func NewManager(db *sql.DB, keys *crypto.Keyring, auditLogger *audit.Logger, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{db: db, keys: keys, audit: auditLogger, logger: logger}
}

type StoreRequest struct {
	OrganizationID string
	ProjectID      *string
	Name           string
	Scope          string
	Description    string
	Value          []byte
	ActorID        string
	ActorType      string
}

type RotationRequest struct {
	SecretID  string
	Value     []byte
	ActorID   string
	ActorType string
}

type DeleteRequest struct {
	SecretID  string
	ActorID   string
	ActorType string
}

type StoredSecret struct {
	Reference models.SecretReference
	Version   int
	KeyID     string
}

func (m *Manager) Store(ctx context.Context, req StoreRequest) (*StoredSecret, error) {
	if err := m.validateReady(); err != nil {
		return nil, err
	}
	ref := models.SecretReference{
		ID:             uuid.New().String(),
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		Name:           req.Name,
		Scope:          req.Scope,
		Provider:       models.SecretProviderEncryptedDB,
		Description:    req.Description,
	}
	if ref.Scope == "" {
		ref.Scope = models.SecretScopeDev
	}
	ref.KeyPath = defaultKeyPathPrefix + ref.ID
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if len(req.Value) == 0 {
		return nil, fmt.Errorf("secret value is required")
	}
	ciphertext, err := m.keys.Encrypt(req.Value, []byte(m.keys.PrimaryID()))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO secret_references (
			id, organization_id, project_id, name, scope, provider, key_path,
			description, last_rotated_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $9)
	`, ref.ID, ref.OrganizationID, nullableString(ref.ProjectID), ref.Name, ref.Scope, ref.Provider, ref.KeyPath, nullIfEmpty(ref.Description), now)
	if err != nil {
		return nil, fmt.Errorf("insert secret reference: %w", err)
	}
	valueID := uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO secret_values (
			id, secret_reference_id, version, key_id, ciphertext, active, created_at, rotated_at
		) VALUES ($1, $2, 1, $3, $4, true, $5, $5)
	`, valueID, ref.ID, m.keys.PrimaryID(), ciphertext, now)
	if err != nil {
		return nil, fmt.Errorf("insert secret value: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	ref.CreatedAt = now
	ref.UpdatedAt = now
	ref.LastRotatedAt = &now
	m.auditEvent(ctx, ref.OrganizationID, req.ActorType, req.ActorID, "secret.write", ref.ID, map[string]any{
		"name":    ref.Name,
		"scope":   ref.Scope,
		"version": 1,
	})
	return &StoredSecret{Reference: ref, Version: 1, KeyID: m.keys.PrimaryID()}, nil
}

func (m *Manager) Rotate(ctx context.Context, req RotationRequest) (*StoredSecret, error) {
	if err := m.validateReady(); err != nil {
		return nil, err
	}
	if req.SecretID == "" {
		return nil, fmt.Errorf("secret id is required")
	}
	if len(req.Value) == 0 {
		return nil, fmt.Errorf("secret value is required")
	}

	ref, version, err := m.loadReferenceAndVersion(ctx, req.SecretID)
	if err != nil {
		return nil, err
	}
	ciphertext, err := m.keys.Encrypt(req.Value, []byte(m.keys.PrimaryID()))
	if err != nil {
		return nil, err
	}
	nextVersion := version + 1
	now := time.Now().UTC()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE secret_values SET active = false WHERE secret_reference_id = $1`, req.SecretID); err != nil {
		return nil, fmt.Errorf("deactivate old secret values: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO secret_values (
			id, secret_reference_id, version, key_id, ciphertext, active, created_at, rotated_at
		) VALUES ($1, $2, $3, $4, $5, true, $6, $6)
	`, uuid.New().String(), req.SecretID, nextVersion, m.keys.PrimaryID(), ciphertext, now); err != nil {
		return nil, fmt.Errorf("insert rotated secret value: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE secret_references SET last_rotated_at = $1, updated_at = $1 WHERE id = $2
	`, now, req.SecretID); err != nil {
		return nil, fmt.Errorf("update secret reference rotation timestamp: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	ref.LastRotatedAt = &now
	ref.UpdatedAt = now
	m.auditEvent(ctx, ref.OrganizationID, req.ActorType, req.ActorID, "secret.rotate", ref.ID, map[string]any{
		"name":    ref.Name,
		"scope":   ref.Scope,
		"version": nextVersion,
	})
	return &StoredSecret{Reference: *ref, Version: nextVersion, KeyID: m.keys.PrimaryID()}, nil
}

func (m *Manager) Delete(ctx context.Context, req DeleteRequest) error {
	if err := m.validateReady(); err != nil {
		return err
	}
	if req.SecretID == "" {
		return fmt.Errorf("secret id is required")
	}

	ref, _, err := m.loadReferenceAndVersion(ctx, req.SecretID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	result, err := m.db.ExecContext(ctx, `
		UPDATE secret_references
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, req.SecretID)
	if err != nil {
		return fmt.Errorf("soft delete secret: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrSecretNotFound
	}

	m.auditEvent(ctx, ref.OrganizationID, req.ActorType, req.ActorID, "secret.delete", ref.ID, map[string]any{
		"name":  ref.Name,
		"scope": ref.Scope,
	})
	return nil
}

func (m *Manager) Resolve(ctx context.Context, secretID, actorType, actorID string) ([]byte, error) {
	if err := m.validateReady(); err != nil {
		return nil, err
	}
	var orgID, name, scope, keyID, ciphertext string
	err := m.db.QueryRowContext(ctx, `
		SELECT sr.organization_id, sr.name, sr.scope, sv.key_id, sv.ciphertext
		FROM secret_references sr
		JOIN secret_values sv ON sv.secret_reference_id = sr.id
		WHERE sr.id = $1 AND sr.deleted_at IS NULL AND sv.active = true
	`, secretID).Scan(&orgID, &name, &scope, &keyID, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, err
	}
	plaintext, err := m.keys.Decrypt(ciphertext, []byte(keyID))
	if err != nil {
		return nil, err
	}
	m.auditEvent(ctx, orgID, actorType, actorID, "secret.read", secretID, map[string]any{
		"name":  name,
		"scope": scope,
	})
	return plaintext, nil
}

// EncryptString encrypts plaintext with the primary key and returns a string of
// the form "keyID:base64ciphertext". The aad is bound to the ciphertext via the
// AES-GCM authentication tag.
func (m *Manager) EncryptString(plaintext, aad string) (string, error) {
	if err := m.validateReady(); err != nil {
		return "", err
	}
	return m.keys.Encrypt([]byte(plaintext), []byte(aad))
}

// DecryptString decrypts a string produced by EncryptString. The aad must match
// the value used during encryption.
func (m *Manager) DecryptString(ciphertext, aad string) (string, error) {
	if err := m.validateReady(); err != nil {
		return "", err
	}
	plaintext, err := m.keys.Decrypt(ciphertext, []byte(aad))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (m *Manager) loadReferenceAndVersion(ctx context.Context, secretID string) (*models.SecretReference, int, error) {
	var ref models.SecretReference
	var projectID, description sql.NullString
	var lastRotated sql.NullTime
	var version int
	err := m.db.QueryRowContext(ctx, `
		SELECT sr.id, sr.organization_id, sr.project_id, sr.name, sr.scope, sr.provider,
		       sr.key_path, sr.description, sr.last_rotated_at, sr.created_at, sr.updated_at,
		       COALESCE(MAX(sv.version), 0)
		FROM secret_references sr
		LEFT JOIN secret_values sv ON sv.secret_reference_id = sr.id
		WHERE sr.id = $1 AND sr.deleted_at IS NULL
		GROUP BY sr.id, sr.organization_id, sr.project_id, sr.name, sr.scope, sr.provider,
		         sr.key_path, sr.description, sr.last_rotated_at, sr.created_at, sr.updated_at
	`, secretID).Scan(
		&ref.ID, &ref.OrganizationID, &projectID, &ref.Name, &ref.Scope, &ref.Provider,
		&ref.KeyPath, &description, &lastRotated, &ref.CreatedAt, &ref.UpdatedAt, &version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrSecretNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	if projectID.Valid {
		ref.ProjectID = &projectID.String
	}
	if description.Valid {
		ref.Description = description.String
	}
	if lastRotated.Valid {
		ref.LastRotatedAt = &lastRotated.Time
	}
	return &ref, version, nil
}

func (m *Manager) auditEvent(ctx context.Context, orgID, actorType, actorID, action, secretID string, details map[string]any) {
	if m.audit == nil {
		return
	}
	if actorType == "" {
		actorType = "system"
	}
	if err := m.audit.LogEvent(ctx, orgID, actorType, actorID, action, "secret", secretID, details); err != nil {
		m.logger.Error("failed to write secret audit log", "error", err, "action", action)
	}
}

func (m *Manager) validateReady() error {
	if m == nil || m.db == nil {
		return fmt.Errorf("secret manager is not configured")
	}
	if m.keys == nil || m.keys.PrimaryID() == "" {
		return fmt.Errorf("%w: keyring is not configured", ErrSecretKey)
	}
	return nil
}

func nullableString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
