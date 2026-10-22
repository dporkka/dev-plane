// Package devices persists device enrollment and revocation in Dev Plane's
// existing SQL database. Callers MUST authenticate the human actor before
// passing a Scope. This package never exposes a remotely executable command.
package devices

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnauthorized      = errors.New("device enrollment not authorized")
	ErrInvalidProof      = errors.New("invalid device enrollment proof")
	ErrNotFound          = errors.New("device not found or not accessible")
	ErrAlreadyEnrolled   = errors.New("device already enrolled; revoked keys may not be reenrolled")
	ErrChallengeConsumed = errors.New("enrollment challenge expired or already consumed")
)

const enrollmentAudience = "dev-plane/device-enrollment/v1"
const maxChallengeSeconds int64 = 120

// Scope MUST come from trusted, authenticated control-plane claims, not JSON.
type Scope struct {
	OrganizationID string
	UserID         string
}

// Challenge is issued by the server, bound to the initiating actor and key.
// It is intentionally independent of the local agent's *session* challenge.
type Challenge struct {
	Nonce          string            `json:"nonce"`
	DeviceID       string            `json:"deviceId"`
	OrganizationID string            `json:"organizationId"`
	UserID         string            `json:"userId"`
	PublicKey      ed25519.PublicKey `json:"publicKey"`
	IssuedUnix     int64             `json:"issuedUnix"`
	ExpiresUnix    int64             `json:"expiresUnix"`
}

func DeviceID(key ed25519.PublicKey) string {
	digest := sha256.Sum256(key)
	return "dp_" + hex.EncodeToString(digest[:])
}

// EnrollmentMessage is an unambiguous domain-separated signing payload.
func EnrollmentMessage(c Challenge) ([]byte, error) {
	if c.Nonce == "" || c.UserID == "" || c.OrganizationID == "" || len(c.PublicKey) != ed25519.PublicKeySize || c.DeviceID != DeviceID(c.PublicKey) || c.IssuedUnix <= 0 || c.ExpiresUnix <= c.IssuedUnix || c.ExpiresUnix-c.IssuedUnix > maxChallengeSeconds {
		return nil, ErrInvalidProof
	}
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n%d\n%d", enrollmentAudience, c.OrganizationID, c.UserID, c.DeviceID, c.Nonce, base64.RawURLEncoding.EncodeToString(c.PublicKey), c.IssuedUnix, c.ExpiresUnix)), nil
}

func VerifyEnrollmentProof(c Challenge, signature []byte, now time.Time) error {
	message, err := EnrollmentMessage(c)
	if err != nil {
		return err
	}
	if now.Unix() < c.IssuedUnix || now.Unix() >= c.ExpiresUnix || len(signature) != ed25519.SignatureSize || !ed25519.Verify(c.PublicKey, message, signature) {
		return ErrInvalidProof
	}
	return nil
}

type Registry struct {
	DB  *sql.DB
	Now func() time.Time
}

func (r Registry) clock() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// member checks current database membership, never trusting actor-supplied org claims alone.
func member(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope Scope) error {
	if scope.UserID == "" || scope.OrganizationID == "" {
		return ErrUnauthorized
	}
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN organizations o ON o.id=u.organization_id WHERE u.id=$1 AND u.organization_id=$2 AND u.deleted_at IS NULL AND o.deleted_at IS NULL)`, scope.UserID, scope.OrganizationID).Scan(&ok)
	if err != nil {
		return fmt.Errorf("device membership verification: %w", err)
	}
	if !ok {
		return ErrUnauthorized
	}
	return nil
}

// BeginEnrollment MUST be invoked only after HTTP authentication. It persists a
// challenge before returning it. It does not enroll an unverified public key.
func (r Registry) BeginEnrollment(ctx context.Context, scope Scope, public ed25519.PublicKey) (Challenge, error) {
	if r.DB == nil {
		return Challenge{}, errors.New("device registry database unavailable")
	}
	if err := member(ctx, r.DB, scope); err != nil {
		return Challenge{}, err
	}
	if len(public) != ed25519.PublicKeySize {
		return Challenge{}, ErrInvalidProof
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return Challenge{}, err
	}
	now := r.clock()
	c := Challenge{Nonce: base64.RawURLEncoding.EncodeToString(buf), DeviceID: DeviceID(public), OrganizationID: scope.OrganizationID, UserID: scope.UserID, PublicKey: append(ed25519.PublicKey(nil), public...), IssuedUnix: now.Unix(), ExpiresUnix: now.Add(60 * time.Second).Unix()}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO device_enrollment_challenges (nonce,device_id,organization_id,user_id,public_key,issued_unix,expires_unix) VALUES ($1,$2,$3,$4,$5,$6,$7)`, c.Nonce, c.DeviceID, c.OrganizationID, c.UserID, []byte(c.PublicKey), c.IssuedUnix, c.ExpiresUnix)
	if err != nil {
		return Challenge{}, fmt.Errorf("persist enrollment challenge: %w", err)
	}
	return c, nil
}

// CompleteEnrollment atomically claims the one-time challenge and inserts the
// enrolled key. Concurrent or replayed attempts cannot both succeed.
func (r Registry) CompleteEnrollment(ctx context.Context, scope Scope, nonce string, signature []byte) (string, error) {
	if r.DB == nil {
		return "", errors.New("device registry database unavailable")
	}
	if nonce == "" || len(signature) != ed25519.SignatureSize {
		return "", ErrInvalidProof
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := member(ctx, tx, scope); err != nil {
		return "", err
	}
	var c Challenge
	c.Nonce = nonce
	err = tx.QueryRowContext(ctx, `SELECT device_id,organization_id,user_id,public_key,issued_unix,expires_unix FROM device_enrollment_challenges WHERE nonce=$1 AND organization_id=$2 AND user_id=$3`, nonce, scope.OrganizationID, scope.UserID).Scan(&c.DeviceID, &c.OrganizationID, &c.UserID, &c.PublicKey, &c.IssuedUnix, &c.ExpiresUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read enrollment challenge: %w", err)
	}
	if err := VerifyEnrollmentProof(c, signature, r.clock()); err != nil {
		return "", err
	}
	now := r.clock().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE device_enrollment_challenges SET consumed_unix=$1 WHERE nonce=$2 AND organization_id=$3 AND user_id=$4 AND consumed_unix IS NULL AND expires_unix>$1`, now, nonce, scope.OrganizationID, scope.UserID)
	if err != nil {
		return "", fmt.Errorf("claim challenge: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", ErrChallengeConsumed
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO enrolled_devices (device_id,organization_id,owner_user_id,public_key) VALUES ($1,$2,$3,$4) ON CONFLICT (device_id) DO NOTHING`, c.DeviceID, c.OrganizationID, c.UserID, []byte(c.PublicKey))
	if err != nil {
		return "", fmt.Errorf("enroll device: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", ErrAlreadyEnrolled
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return c.DeviceID, nil
}

// Revoke allows only the active device's enrolling owner in the same org to
// revoke it. A revoked key must not regain authority on reconnect.
func (r Registry) Revoke(ctx context.Context, scope Scope, deviceID string) error {
	if r.DB == nil {
		return errors.New("device registry database unavailable")
	}
	if err := member(ctx, r.DB, scope); err != nil {
		return err
	}
	if deviceID == "" {
		return ErrNotFound
	}
	result, err := r.DB.ExecContext(ctx, `UPDATE enrolled_devices SET revoked_at=CURRENT_TIMESTAMP WHERE device_id=$1 AND organization_id=$2 AND owner_user_id=$3 AND revoked_at IS NULL`, deviceID, scope.OrganizationID, scope.UserID)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// ActiveKey is ONLY for a trusted gateway verifying device signatures.
// It must be checked on every new session and operation; don't cache beyond a
// request without a revocation-invalidated cache.
func (r Registry) ActiveKey(ctx context.Context, deviceID string) (ed25519.PublicKey, error) {
	if r.DB == nil {
		return nil, errors.New("device registry database unavailable")
	}
	if deviceID == "" {
		return nil, ErrNotFound
	}
	var key []byte
	err := r.DB.QueryRowContext(ctx, `SELECT d.public_key FROM enrolled_devices d JOIN users u ON u.id=d.owner_user_id AND u.organization_id=d.organization_id JOIN organizations o ON o.id=d.organization_id WHERE d.device_id=$1 AND d.revoked_at IS NULL AND u.deleted_at IS NULL AND o.deleted_at IS NULL`, deviceID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(key) != ed25519.PublicKeySize || DeviceID(key) != deviceID {
		return nil, ErrInvalidProof
	}
	return ed25519.PublicKey(key), nil
}
