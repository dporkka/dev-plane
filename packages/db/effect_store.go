package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ai-dev-control-plane/execution"
)

var ErrEffectIntentNotFound = errors.New("execution effect intent not found")

type EffectRecord struct {
	Intent      execution.EffectIntent
	Receipt     *execution.EffectReceipt
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// EnsureEffectIntent durably records an effect intent before an external side
// effect executes. Repeating the exact same intent is idempotent; reusing the
// same logical effect identity with changed authority or input fails closed.
func EnsureEffectIntent(ctx context.Context, database *sql.DB, intent execution.EffectIntent) error {
	if database == nil {
		return errors.New("database is required")
	}
	if err := intent.Validate(); err != nil {
		return err
	}
	if uint64(intent.Epoch) > math.MaxInt64 {
		return fmt.Errorf("effect epoch exceeds database range")
	}

	now := time.Now().UTC()
	_, err := database.ExecContext(ctx, `
		INSERT INTO execution_effects (
			effect_id, run_id, activation_id, epoch, ordinal,
			operation, resource, revision, input_digest, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT(effect_id) DO NOTHING
	`,
		string(intent.ID),
		intent.RunID,
		intent.ActivationID,
		int64(intent.Epoch),
		int64(intent.Ordinal),
		intent.Grant.Operation,
		intent.Grant.Resource,
		intent.Grant.Revision,
		intent.InputDigest,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert effect intent %s: %w", intent.ID, err)
	}

	record, err := LoadEffect(ctx, database, intent.ID)
	if err != nil {
		return err
	}
	if _, err := execution.MergeIntent(record.Intent, intent); err != nil {
		return err
	}
	return nil
}

// LoadEffect loads the durable intent and optional receipt for one logical
// external effect.
func LoadEffect(ctx context.Context, database *sql.DB, effectID execution.OperationID) (EffectRecord, error) {
	if database == nil {
		return EffectRecord{}, errors.New("database is required")
	}
	if effectID == "" {
		return EffectRecord{}, execution.ErrInvalidEffect
	}

	var (
		runID, activationID, operation, resource, revision, inputDigest string
		epoch, ordinal                                                   int64
		provider, reference, outputDigest                               sql.NullString
		createdAt                                                        time.Time
		completedAt                                                      sql.NullTime
	)
	err := database.QueryRowContext(ctx, `
		SELECT run_id, activation_id, epoch, ordinal,
		       operation, resource, revision, input_digest,
		       provider, reference, output_digest, created_at, completed_at
		FROM execution_effects
		WHERE effect_id = $1
	`, string(effectID)).Scan(
		&runID,
		&activationID,
		&epoch,
		&ordinal,
		&operation,
		&resource,
		&revision,
		&inputDigest,
		&provider,
		&reference,
		&outputDigest,
		&createdAt,
		&completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return EffectRecord{}, fmt.Errorf("%w: %s", ErrEffectIntentNotFound, effectID)
	}
	if err != nil {
		return EffectRecord{}, fmt.Errorf("load effect %s: %w", effectID, err)
	}
	if epoch <= 0 || ordinal < 0 || ordinal > math.MaxUint32 {
		return EffectRecord{}, fmt.Errorf("invalid persisted effect coordinates for %s", effectID)
	}

	intent := execution.EffectIntent{
		ID:           effectID,
		RunID:        runID,
		ActivationID: activationID,
		Epoch:        execution.Epoch(epoch),
		Ordinal:      uint32(ordinal),
		Grant: execution.Grant{
			Operation: operation,
			Resource:  resource,
			Revision:  revision,
		},
		InputDigest: inputDigest,
	}
	if err := intent.Validate(); err != nil {
		return EffectRecord{}, fmt.Errorf("validate persisted effect intent %s: %w", effectID, err)
	}

	record := EffectRecord{
		Intent:    intent,
		CreatedAt: createdAt,
	}
	if completedAt.Valid {
		completed := completedAt.Time
		record.CompletedAt = &completed
	}

	if provider.Valid || outputDigest.Valid || reference.Valid || completedAt.Valid {
		if !provider.Valid || !outputDigest.Valid || !completedAt.Valid {
			return EffectRecord{}, fmt.Errorf("incomplete persisted effect receipt for %s", effectID)
		}
		receipt := execution.EffectReceipt{
			EffectID:     effectID,
			Provider:     provider.String,
			OutputDigest: outputDigest.String,
		}
		if reference.Valid {
			receipt.Reference = reference.String
		}
		if err := receipt.Validate(); err != nil {
			return EffectRecord{}, fmt.Errorf("validate persisted effect receipt %s: %w", effectID, err)
		}
		record.Receipt = &receipt
	}

	return record, nil
}

// RecordEffectReceipt atomically records the externally observed result. The
// exact same receipt can be replayed; a conflicting receipt for the same effect
// identity is rejected.
func RecordEffectReceipt(ctx context.Context, database *sql.DB, receipt execution.EffectReceipt) error {
	if database == nil {
		return errors.New("database is required")
	}
	if err := receipt.Validate(); err != nil {
		return err
	}

	record, err := LoadEffect(ctx, database, receipt.EffectID)
	if err != nil {
		return err
	}
	if record.Receipt != nil {
		_, err := execution.MergeReceipt(*record.Receipt, receipt)
		return err
	}

	now := time.Now().UTC()
	result, err := database.ExecContext(ctx, `
		UPDATE execution_effects
		SET provider = $1,
		    reference = $2,
		    output_digest = $3,
		    completed_at = $4
		WHERE effect_id = $5
		  AND provider IS NULL
		  AND output_digest IS NULL
		  AND completed_at IS NULL
	`, receipt.Provider, receipt.Reference, receipt.OutputDigest, now, string(receipt.EffectID))
	if err != nil {
		return fmt.Errorf("record effect receipt %s: %w", receipt.EffectID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check effect receipt %s update: %w", receipt.EffectID, err)
	}
	if affected == 1 {
		return nil
	}

	record, err = LoadEffect(ctx, database, receipt.EffectID)
	if err != nil {
		return err
	}
	if record.Receipt == nil {
		return fmt.Errorf("effect receipt %s was not recorded", receipt.EffectID)
	}
	_, err = execution.MergeReceipt(*record.Receipt, receipt)
	return err
}
