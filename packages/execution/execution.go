// Package execution defines provider-neutral identity, authority, fencing, and
// external-effect recovery primitives for Dev Plane execution.
//
// The package deliberately contains no scheduler, workflow engine, transport, or
// persistence implementation. Callers own storage and delivery; these values
// define the invariants those layers must preserve.
package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrInvalidActivation   = errors.New("invalid execution activation")
	ErrEpochExhausted      = errors.New("execution epoch exhausted")
	ErrInvalidOperation    = errors.New("invalid operation identity")
	ErrInvalidGrant        = errors.New("invalid authority grant")
	ErrInvalidManifest     = errors.New("invalid authority manifest")
	ErrAuthorityEscalation = errors.New("authority delegation would escalate privileges")
	ErrActivationMismatch  = errors.New("execution activation mismatch")
	ErrStaleEpoch          = errors.New("stale execution epoch")
	ErrInvalidEffect       = errors.New("invalid effect")
	ErrReceiptMismatch     = errors.New("effect receipt does not match intent")
	ErrReceiptConflict     = errors.New("conflicting effect receipt")
)

type Epoch uint64

type Activation struct {
	RunID string
	ID    string
	Epoch Epoch
}

func (a Activation) Validate() error {
	if invalidToken(a.RunID) || invalidToken(a.ID) || a.Epoch == 0 {
		return ErrInvalidActivation
	}
	return nil
}

func (a Activation) AdvanceEpoch() (Activation, error) {
	if err := a.Validate(); err != nil {
		return Activation{}, err
	}
	if uint64(a.Epoch) == math.MaxUint64 {
		return Activation{}, ErrEpochExhausted
	}
	a.Epoch++
	return a, nil
}

type OperationKind string

const (
	OperationEvent  OperationKind = "event"
	OperationEffect OperationKind = "effect"
)

type OperationID string

func NewOperationID(activation Activation, kind OperationKind, ordinal uint32) (OperationID, error) {
	if err := activation.Validate(); err != nil {
		return "", err
	}
	if kind != OperationEvent && kind != OperationEffect {
		return "", ErrInvalidOperation
	}

	// Epoch is intentionally excluded. A new owner must fence stale writers with
	// a higher epoch while retaining the same logical operation identity for
	// replay and external idempotency.
	payload := strings.Join([]string{
		"dev-plane/execution/v1",
		activation.RunID,
		activation.ID,
		string(kind),
		strconv.FormatUint(uint64(ordinal), 10),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return OperationID("dp1_" + hex.EncodeToString(sum[:])), nil
}

type Grant struct {
	Operation string
	Resource  string
	Revision  string
}

func (g Grant) Validate() error {
	if invalidToken(g.Operation) || invalidToken(g.Resource) || strings.ContainsRune(g.Revision, '\x00') {
		return ErrInvalidGrant
	}
	return nil
}

func (g Grant) canonical() string {
	return strings.Join([]string{g.Operation, g.Resource, g.Revision}, "\x00")
}

type Manifest struct {
	Grants []Grant
}

func NewManifest(grants ...Grant) (Manifest, error) {
	manifest := Manifest{Grants: append([]Grant(nil), grants...)}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}

	sort.Slice(manifest.Grants, func(i, j int) bool {
		return manifest.Grants[i].canonical() < manifest.Grants[j].canonical()
	})
	if len(manifest.Grants) < 2 {
		return manifest, nil
	}

	deduped := manifest.Grants[:0]
	for _, grant := range manifest.Grants {
		if len(deduped) == 0 || deduped[len(deduped)-1] != grant {
			deduped = append(deduped, grant)
		}
	}
	manifest.Grants = deduped
	return manifest, nil
}

func (m Manifest) Validate() error {
	for i, grant := range m.Grants {
		if err := grant.Validate(); err != nil {
			return fmt.Errorf("%w: grant %d: %v", ErrInvalidManifest, i, err)
		}
	}
	return nil
}

func (m Manifest) Allows(grant Grant) (bool, error) {
	if err := m.Validate(); err != nil {
		return false, err
	}
	if err := grant.Validate(); err != nil {
		return false, err
	}
	for _, held := range m.Grants {
		if held == grant {
			return true, nil
		}
	}
	return false, nil
}

func (m Manifest) Delegate(requested Manifest) (Manifest, error) {
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := requested.Validate(); err != nil {
		return Manifest{}, err
	}

	for _, grant := range requested.Grants {
		allowed, err := m.Allows(grant)
		if err != nil {
			return Manifest{}, err
		}
		if !allowed {
			return Manifest{}, fmt.Errorf("%w: %s on %s", ErrAuthorityEscalation, grant.Operation, grant.Resource)
		}
	}
	return NewManifest(requested.Grants...)
}

func (m Manifest) Digest() (string, error) {
	canonical, err := NewManifest(m.Grants...)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("dev-plane/authority/v1\x00"))
	for _, grant := range canonical.Grants {
		_, _ = h.Write([]byte(grant.canonical()))
		_, _ = h.Write([]byte{0xff})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type EffectIntent struct {
	ID           OperationID
	RunID        string
	ActivationID string
	Epoch        Epoch
	Ordinal      uint32
	Grant        Grant
	InputDigest  string
}

func NewEffectIntent(activation Activation, ordinal uint32, grant Grant, input []byte) (EffectIntent, error) {
	if err := activation.Validate(); err != nil {
		return EffectIntent{}, err
	}
	if err := grant.Validate(); err != nil {
		return EffectIntent{}, err
	}
	id, err := NewOperationID(activation, OperationEffect, ordinal)
	if err != nil {
		return EffectIntent{}, err
	}
	return EffectIntent{
		ID:           id,
		RunID:        activation.RunID,
		ActivationID: activation.ID,
		Epoch:        activation.Epoch,
		Ordinal:      ordinal,
		Grant:        grant,
		InputDigest:  digestBytes("dev-plane/effect-input/v1", input),
	}, nil
}

func (i EffectIntent) Validate() error {
	activation := Activation{RunID: i.RunID, ID: i.ActivationID, Epoch: i.Epoch}
	if err := activation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEffect, err)
	}
	if err := i.Grant.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEffect, err)
	}
	if i.InputDigest == "" {
		return ErrInvalidEffect
	}
	expected, err := NewOperationID(activation, OperationEffect, i.Ordinal)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEffect, err)
	}
	if i.ID != expected {
		return fmt.Errorf("%w: operation id mismatch", ErrInvalidEffect)
	}
	return nil
}

func (i EffectIntent) AuthorizeAgainst(current Activation) error {
	if err := i.Validate(); err != nil {
		return err
	}
	if err := current.Validate(); err != nil {
		return err
	}
	if i.RunID != current.RunID || i.ActivationID != current.ID {
		return ErrActivationMismatch
	}
	if i.Epoch != current.Epoch {
		return ErrStaleEpoch
	}
	return nil
}

type EffectReceipt struct {
	EffectID     OperationID
	Provider     string
	Reference    string
	OutputDigest string
}

func NewEffectReceipt(intent EffectIntent, provider, reference string, output []byte) (EffectReceipt, error) {
	if err := intent.Validate(); err != nil {
		return EffectReceipt{}, err
	}
	if invalidToken(provider) {
		return EffectReceipt{}, ErrReceiptMismatch
	}
	return EffectReceipt{
		EffectID:     intent.ID,
		Provider:     provider,
		Reference:    reference,
		OutputDigest: digestBytes("dev-plane/effect-output/v1", output),
	}, nil
}

func (r EffectReceipt) Validate() error {
	if r.EffectID == "" || invalidToken(r.Provider) || r.OutputDigest == "" {
		return ErrReceiptMismatch
	}
	return nil
}

type EffectRecoveryAction string

const (
	EffectExecute              EffectRecoveryAction = "execute"
	EffectReplayRecordedResult EffectRecoveryAction = "replay_recorded_result"
)

func RecoverEffect(intent EffectIntent, receipt *EffectReceipt) (EffectRecoveryAction, error) {
	if err := intent.Validate(); err != nil {
		return "", err
	}
	if receipt == nil {
		return EffectExecute, nil
	}
	if err := receipt.Validate(); err != nil {
		return "", err
	}
	if receipt.EffectID != intent.ID {
		return "", ErrReceiptMismatch
	}
	return EffectReplayRecordedResult, nil
}

func MergeReceipt(existing, incoming EffectReceipt) (EffectReceipt, error) {
	if err := existing.Validate(); err != nil {
		return EffectReceipt{}, err
	}
	if err := incoming.Validate(); err != nil {
		return EffectReceipt{}, err
	}
	if existing == incoming {
		return existing, nil
	}
	return EffectReceipt{}, ErrReceiptConflict
}

func digestBytes(domain string, value []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(value)
	return hex.EncodeToString(h.Sum(nil))
}

func invalidToken(value string) bool {
	return strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00')
}
