package db

import (
	"context"
	"errors"
	"testing"

	"github.com/ai-dev-control-plane/execution"
)

func TestEnsureEffectIntentIsIdempotentAndRejectsPayloadDrift(t *testing.T) {
	database := openEffectStoreTestDB(t)
	activation := execution.Activation{RunID: "run-1", ID: "activation-1", Epoch: 1}
	grant := execution.Grant{
		Operation: "forge.merge",
		Resource:  "repo:dporkka/dev-plane/pr:123",
		Revision:  "abc123",
	}
	intent, err := execution.NewEffectIntent(activation, 0, grant, []byte("merge head abc123"))
	if err != nil {
		t.Fatalf("NewEffectIntent() error = %v", err)
	}

	if err := EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent(first) error = %v", err)
	}
	if err := EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent(idempotent) error = %v", err)
	}

	changed, err := execution.NewEffectIntent(activation, 0, grant, []byte("merge head def456"))
	if err != nil {
		t.Fatalf("NewEffectIntent(changed) error = %v", err)
	}
	if changed.ID != intent.ID {
		t.Fatal("same logical effect ordinal must keep one identity")
	}
	if err := EnsureEffectIntent(context.Background(), database.DB, changed); !errors.Is(err, execution.ErrIntentConflict) {
		t.Fatalf("EnsureEffectIntent(payload drift) error = %v, want %v", err, execution.ErrIntentConflict)
	}

	record, err := LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Intent != intent {
		t.Fatalf("stored intent = %#v, want %#v", record.Intent, intent)
	}
	if record.Receipt != nil {
		t.Fatalf("stored receipt = %#v, want nil", record.Receipt)
	}
}

func TestRecordEffectReceiptIsIdempotentAndRejectsConflicts(t *testing.T) {
	database := openEffectStoreTestDB(t)
	activation := execution.Activation{RunID: "run-1", ID: "activation-1", Epoch: 1}
	grant := execution.Grant{
		Operation: "forge.merge",
		Resource:  "repo:dporkka/dev-plane/pr:123",
		Revision:  "abc123",
	}
	intent, err := execution.NewEffectIntent(activation, 0, grant, []byte("merge head abc123"))
	if err != nil {
		t.Fatalf("NewEffectIntent() error = %v", err)
	}
	if err := EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent() error = %v", err)
	}

	receipt, err := execution.NewEffectReceipt(intent, "github", "merge-sha-1", []byte("merged"))
	if err != nil {
		t.Fatalf("NewEffectReceipt() error = %v", err)
	}
	if err := RecordEffectReceipt(context.Background(), database.DB, receipt); err != nil {
		t.Fatalf("RecordEffectReceipt(first) error = %v", err)
	}
	if err := RecordEffectReceipt(context.Background(), database.DB, receipt); err != nil {
		t.Fatalf("RecordEffectReceipt(idempotent) error = %v", err)
	}

	conflict := receipt
	conflict.Reference = "merge-sha-2"
	if err := RecordEffectReceipt(context.Background(), database.DB, conflict); !errors.Is(err, execution.ErrReceiptConflict) {
		t.Fatalf("RecordEffectReceipt(conflict) error = %v, want %v", err, execution.ErrReceiptConflict)
	}

	record, err := LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Receipt == nil || *record.Receipt != receipt {
		t.Fatalf("stored receipt = %#v, want %#v", record.Receipt, receipt)
	}
}

func TestRecordEffectReceiptRejectsMissingIntent(t *testing.T) {
	database := openEffectStoreTestDB(t)
	receipt := execution.EffectReceipt{
		EffectID:     execution.OperationID("dp1_missing"),
		Provider:     "github",
		Reference:    "merge-sha",
		OutputDigest: "digest",
	}
	if err := RecordEffectReceipt(context.Background(), database.DB, receipt); !errors.Is(err, ErrEffectIntentNotFound) {
		t.Fatalf("RecordEffectReceipt(missing) error = %v, want %v", err, ErrEffectIntentNotFound)
	}
}

func openEffectStoreTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New(:memory:) error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`
		CREATE TABLE execution_effects (
			effect_id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			activation_id TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			ordinal INTEGER NOT NULL,
			operation TEXT NOT NULL,
			resource TEXT NOT NULL,
			revision TEXT NOT NULL DEFAULT '',
			input_digest TEXT NOT NULL,
			provider TEXT,
			reference TEXT,
			output_digest TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("create execution_effects: %v", err)
	}
	return database
}
