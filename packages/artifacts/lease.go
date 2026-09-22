package artifacts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrLeaseHeld     = errors.New("artifact write lease is held")
	ErrLeaseNotFound = errors.New("artifact write lease not found")
	ErrLeaseExpired  = errors.New("artifact write lease expired")
	ErrLeaseToken    = errors.New("artifact write lease token does not match")
)

const MaxLeaseTTL = 24 * time.Hour

type Lease struct {
	ScopeID    string    `json:"scope_id"`
	Path       string    `json:"path"`
	OwnerID    string    `json:"owner_id"`
	Token      string    `json:"token"`
	Generation uint64    `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type AcquireLeaseRequest struct {
	ScopeID string
	Path    string
	OwnerID string
	TTL     time.Duration
}

type LeaseStore interface {
	Acquire(ctx context.Context, req AcquireLeaseRequest) (Lease, error)
	Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error)
	Release(ctx context.Context, lease Lease) error
	Get(ctx context.Context, scopeID, path string) (Lease, error)
	Validate(ctx context.Context, lease Lease) error
}

type leaseSlot struct {
	generation uint64
	active     *Lease
}

type MemoryLeaseStore struct {
	mu    sync.Mutex
	slots map[string]*leaseSlot
	now   func() time.Time
}

func NewMemoryLeaseStore() *MemoryLeaseStore {
	return &MemoryLeaseStore{
		slots: map[string]*leaseSlot{},
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemoryLeaseStore) Acquire(ctx context.Context, req AcquireLeaseRequest) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	path, err := validateLeaseRequest(req.ScopeID, req.Path, req.OwnerID, req.TTL)
	if err != nil {
		return Lease{}, err
	}
	now := s.now()
	key := leaseKey(req.ScopeID, path)

	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[key]
	if slot == nil {
		slot = &leaseSlot{}
		s.slots[key] = slot
	}
	if slot.active != nil && slot.active.ExpiresAt.After(now) {
		return Lease{}, fmt.Errorf("%w: %s owned by %s until %s",
			ErrLeaseHeld, path, slot.active.OwnerID, slot.active.ExpiresAt.Format(time.RFC3339))
	}
	slot.generation++
	token, err := newLeaseToken()
	if err != nil {
		return Lease{}, err
	}
	lease := Lease{
		ScopeID: req.ScopeID,
		Path: path,
		OwnerID: req.OwnerID,
		Token: token,
		Generation: slot.generation,
		ExpiresAt: now.Add(req.TTL),
	}
	slot.active = &lease
	return lease, nil
}

func (s *MemoryLeaseStore) Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	if err := validateTTL(ttl); err != nil {
		return Lease{}, err
	}
	now := s.now()
	key := leaseKey(lease.ScopeID, lease.Path)

	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[key]
	if slot == nil || slot.active == nil {
		return Lease{}, ErrLeaseNotFound
	}
	if !slot.active.ExpiresAt.After(now) {
		slot.active = nil
		return Lease{}, ErrLeaseExpired
	}
	if slot.active.Token != lease.Token || slot.active.Generation != lease.Generation {
		return Lease{}, ErrLeaseToken
	}
	renewed := *slot.active
	renewed.ExpiresAt = now.Add(ttl)
	slot.active = &renewed
	return renewed, nil
}

func (s *MemoryLeaseStore) Release(ctx context.Context, lease Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := leaseKey(lease.ScopeID, lease.Path)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[key]
	if slot == nil || slot.active == nil {
		return ErrLeaseNotFound
	}
	if !slot.active.ExpiresAt.After(now) {
		slot.active = nil
		return ErrLeaseExpired
	}
	if slot.active.Token != lease.Token || slot.active.Generation != lease.Generation {
		return ErrLeaseToken
	}
	slot.active = nil
	return nil
}

func (s *MemoryLeaseStore) Get(ctx context.Context, scopeID, artifactPath string) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	path, err := NormalizeArtifactPath(artifactPath)
	if err != nil {
		return Lease{}, err
	}
	key := leaseKey(scopeID, path)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[key]
	if slot == nil || slot.active == nil {
		return Lease{}, ErrLeaseNotFound
	}
	if !slot.active.ExpiresAt.After(now) {
		slot.active = nil
		return Lease{}, ErrLeaseExpired
	}
	visible := *slot.active
	visible.Token = ""
	return visible, nil
}

func (s *MemoryLeaseStore) Validate(ctx context.Context, lease Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := NormalizeArtifactPath(lease.Path)
	if err != nil {
		return err
	}
	now := s.now()
	key := leaseKey(lease.ScopeID, path)
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[key]
	if slot == nil || slot.active == nil {
		return ErrLeaseNotFound
	}
	if !slot.active.ExpiresAt.After(now) {
		slot.active = nil
		return ErrLeaseExpired
	}
	if slot.active.Token != lease.Token || slot.active.Generation != lease.Generation {
		return ErrLeaseToken
	}
	if lease.OwnerID != "" && slot.active.OwnerID != lease.OwnerID {
		return ErrLeaseToken
	}
	return nil
}

func validateLeaseRequest(scopeID, artifactPath, ownerID string, ttl time.Duration) (string, error) {
	if strings.TrimSpace(scopeID) == "" || strings.ContainsRune(scopeID, '\x00') {
		return "", fmt.Errorf("artifact lease scope ID is invalid")
	}
	if strings.TrimSpace(ownerID) == "" || strings.ContainsRune(ownerID, '\x00') {
		return "", fmt.Errorf("artifact lease owner ID is invalid")
	}
	path, err := NormalizeArtifactPath(artifactPath)
	if err != nil {
		return "", err
	}
	if err := validateTTL(ttl); err != nil {
		return "", err
	}
	return path, nil
}

func validateTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("artifact lease TTL must be positive")
	}
	if ttl > MaxLeaseTTL {
		return fmt.Errorf("artifact lease TTL exceeds maximum %s", MaxLeaseTTL)
	}
	return nil
}

func leaseKey(scopeID, path string) string {
	return scopeID + "\x00" + path
}

func newLeaseToken() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate artifact lease token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}


var _ LeaseStore = (*MemoryLeaseStore)(nil)
