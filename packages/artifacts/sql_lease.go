package artifacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type SQLDialect string

const (
	SQLDialectSQLite   SQLDialect = "sqlite"
	SQLDialectPostgres SQLDialect = "postgres"
)

// SQLLeaseStore is the durable production lease implementation. Acquisition is
// one atomic INSERT ... ON CONFLICT ... WHERE ... RETURNING statement, so
// competing swarm workers cannot both win an expired lease slot.
type SQLLeaseStore struct {
	db      *sql.DB
	dialect SQLDialect
	now     func() time.Time
}

func NewSQLLeaseStore(db *sql.DB, dialect SQLDialect) (*SQLLeaseStore, error) {
	if db == nil {
		return nil, fmt.Errorf("artifact lease database is required")
	}
	if dialect != SQLDialectSQLite && dialect != SQLDialectPostgres {
		return nil, fmt.Errorf("unsupported artifact lease SQL dialect %q", dialect)
	}
	return &SQLLeaseStore{
		db:      db,
		dialect: dialect,
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *SQLLeaseStore) Acquire(ctx context.Context, req AcquireLeaseRequest) (Lease, error) {
	path, err := validateLeaseRequest(req.ScopeID, req.Path, req.OwnerID, req.TTL)
	if err != nil {
		return Lease{}, err
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	token, err := newLeaseToken()
	if err != nil {
		return Lease{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(req.TTL)
	tokenHash := hashLeaseToken(token)

	query := fmt.Sprintf(`
INSERT INTO artifact_leases (
    scope_id, artifact_path, owner_id, token_hash, generation,
    expires_at, created_at, updated_at
) VALUES (%s, %s, %s, %s, 1, %s, %s, %s)
ON CONFLICT (scope_id, artifact_path) DO UPDATE SET
    owner_id = excluded.owner_id,
    token_hash = excluded.token_hash,
    generation = artifact_leases.generation + 1,
    expires_at = excluded.expires_at,
    updated_at = excluded.updated_at
WHERE artifact_leases.expires_at <= excluded.updated_at
RETURNING generation
`,
		s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4),
		s.placeholder(5), s.placeholder(6), s.placeholder(7),
	)

	var generation uint64
	err = s.db.QueryRowContext(
		ctx,
		query,
		req.ScopeID,
		path,
		req.OwnerID,
		tokenHash,
		expiresAt,
		now,
		now,
	).Scan(&generation)
	if err == nil {
		return Lease{
			ScopeID: req.ScopeID, Path: path, OwnerID: req.OwnerID, Token: token,
			Generation: generation, ExpiresAt: expiresAt,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Lease{}, fmt.Errorf("acquire artifact lease: %w", err)
	}

	current, currentErr := s.getRow(ctx, req.ScopeID, path)
	if currentErr == nil && current.ExpiresAt.After(now) {
		return Lease{}, fmt.Errorf("%w: %s owned by %s until %s",
			ErrLeaseHeld, path, current.OwnerID, current.ExpiresAt.Format(time.RFC3339))
	}
	if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
		return Lease{}, fmt.Errorf("inspect held artifact lease: %w", currentErr)
	}
	return Lease{}, ErrLeaseHeld
}

func (s *SQLLeaseStore) Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error) {
	if err := validateTTL(ttl); err != nil {
		return Lease{}, err
	}
	if lease.ScopeID == "" || lease.Path == "" || lease.Token == "" || lease.Generation == 0 {
		return Lease{}, ErrLeaseToken
	}
	path, err := NormalizeArtifactPath(lease.Path)
	if err != nil {
		return Lease{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(ttl)
	query := fmt.Sprintf(`
UPDATE artifact_leases
SET expires_at = %s, updated_at = %s
WHERE scope_id = %s
  AND artifact_path = %s
  AND generation = %s
  AND token_hash = %s
  AND expires_at > %s
RETURNING owner_id
`,
		s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4),
		s.placeholder(5), s.placeholder(6), s.placeholder(7),
	)
	var ownerID string
	err = s.db.QueryRowContext(
		ctx,
		query,
		expiresAt,
		now,
		lease.ScopeID,
		path,
		lease.Generation,
		hashLeaseToken(lease.Token),
		now,
	).Scan(&ownerID)
	if err == nil {
		lease.Path = path
		lease.OwnerID = ownerID
		lease.ExpiresAt = expiresAt
		return lease, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Lease{}, fmt.Errorf("renew artifact lease: %w", err)
	}
	return Lease{}, s.classifyLeaseMiss(ctx, lease.ScopeID, path, lease.Generation, lease.Token, now)
}

func (s *SQLLeaseStore) Release(ctx context.Context, lease Lease) error {
	if lease.ScopeID == "" || lease.Path == "" || lease.Token == "" || lease.Generation == 0 {
		return ErrLeaseToken
	}
	path, err := NormalizeArtifactPath(lease.Path)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	query := fmt.Sprintf(`
UPDATE artifact_leases
SET owner_id = '', token_hash = '', expires_at = %s, updated_at = %s
WHERE scope_id = %s
  AND artifact_path = %s
  AND generation = %s
  AND token_hash = %s
  AND expires_at > %s
`,
		s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4),
		s.placeholder(5), s.placeholder(6), s.placeholder(7),
	)
	result, err := s.db.ExecContext(
		ctx,
		query,
		now,
		now,
		lease.ScopeID,
		path,
		lease.Generation,
		hashLeaseToken(lease.Token),
		now,
	)
	if err != nil {
		return fmt.Errorf("release artifact lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("artifact lease rows affected: %w", err)
	}
	if rows == 1 {
		return nil
	}
	return s.classifyLeaseMiss(ctx, lease.ScopeID, path, lease.Generation, lease.Token, now)
}

func (s *SQLLeaseStore) Validate(ctx context.Context, lease Lease) error {
	if lease.ScopeID == "" || lease.Path == "" || lease.Token == "" || lease.Generation == 0 {
		return ErrLeaseToken
	}
	path, err := NormalizeArtifactPath(lease.Path)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	row, err := s.getRow(ctx, lease.ScopeID, path)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseNotFound
	}
	if err != nil {
		return fmt.Errorf("validate artifact lease: %w", err)
	}
	if row.OwnerID == "" || row.TokenHash == "" {
		return ErrLeaseNotFound
	}
	if !row.ExpiresAt.After(now) {
		return ErrLeaseExpired
	}
	if row.Generation != lease.Generation || row.TokenHash != hashLeaseToken(lease.Token) {
		return ErrLeaseToken
	}
	if lease.OwnerID != "" && row.OwnerID != lease.OwnerID {
		return ErrLeaseToken
	}
	return nil
}

func (s *SQLLeaseStore) Get(ctx context.Context, scopeID, artifactPath string) (Lease, error) {
	if scopeID == "" {
		return Lease{}, fmt.Errorf("artifact lease scope ID is required")
	}
	path, err := NormalizeArtifactPath(artifactPath)
	if err != nil {
		return Lease{}, err
	}
	row, err := s.getRow(ctx, scopeID, path)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Lease{}, ErrLeaseNotFound
		}
		return Lease{}, fmt.Errorf("get artifact lease: %w", err)
	}
	now := s.now().UTC()
	if row.OwnerID == "" || row.TokenHash == "" {
		return Lease{}, ErrLeaseNotFound
	}
	if !row.ExpiresAt.After(now) {
		return Lease{}, ErrLeaseExpired
	}
	return Lease{
		ScopeID: scopeID,
		Path: path,
		OwnerID: row.OwnerID,
		Generation: row.Generation,
		ExpiresAt: row.ExpiresAt,
	}, nil
}

type sqlLeaseRow struct {
	OwnerID    string
	TokenHash  string
	Generation uint64
	ExpiresAt  time.Time
}

func (s *SQLLeaseStore) getRow(ctx context.Context, scopeID, path string) (sqlLeaseRow, error) {
	query := fmt.Sprintf(`
SELECT owner_id, token_hash, generation, expires_at
FROM artifact_leases
WHERE scope_id = %s AND artifact_path = %s
`, s.placeholder(1), s.placeholder(2))
	var (
		row       sqlLeaseRow
		expiresAt any
	)
	err := s.db.QueryRowContext(ctx, query, scopeID, path).Scan(
		&row.OwnerID, &row.TokenHash, &row.Generation, &expiresAt,
	)
	if err != nil {
		return sqlLeaseRow{}, err
	}
	row.ExpiresAt, err = parseSQLLeaseTime(expiresAt)
	if err != nil {
		return sqlLeaseRow{}, err
	}
	return row, nil
}

func (s *SQLLeaseStore) classifyLeaseMiss(
	ctx context.Context,
	scopeID, path string,
	generation uint64,
	token string,
	now time.Time,
) error {
	row, err := s.getRow(ctx, scopeID, path)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseNotFound
	}
	if err != nil {
		return fmt.Errorf("inspect artifact lease: %w", err)
	}
	if row.OwnerID == "" || row.TokenHash == "" {
		return ErrLeaseNotFound
	}
	if !row.ExpiresAt.After(now) {
		return ErrLeaseExpired
	}
	if row.Generation != generation || row.TokenHash != hashLeaseToken(token) {
		return ErrLeaseToken
	}
	return ErrLeaseToken
}

func (s *SQLLeaseStore) placeholder(index int) string {
	if s.dialect == SQLDialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func hashLeaseToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}


func parseSQLLeaseTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseSQLLeaseTimeString(typed)
	case []byte:
		return parseSQLLeaseTimeString(string(typed))
	default:
		return time.Time{}, fmt.Errorf("unsupported artifact lease timestamp type %T", value)
	}
}

func parseSQLLeaseTimeString(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999+00:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse artifact lease timestamp %q", value)
}

var _ LeaseStore = (*SQLLeaseStore)(nil)
