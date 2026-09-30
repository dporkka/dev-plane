package execution

import (
	"errors"
	"math"
	"testing"
)

func TestOperationIDIsStableAcrossOwnershipEpochChanges(t *testing.T) {
	activation := Activation{RunID: "run-1", ID: "activation-1", Epoch: 4}
	first, err := NewOperationID(activation, OperationEffect, 7)
	if err != nil {
		t.Fatalf("NewOperationID() error = %v", err)
	}

	next, err := activation.AdvanceEpoch()
	if err != nil {
		t.Fatalf("AdvanceEpoch() error = %v", err)
	}
	second, err := NewOperationID(next, OperationEffect, 7)
	if err != nil {
		t.Fatalf("NewOperationID() after takeover error = %v", err)
	}

	if first != second {
		t.Fatalf("operation ID changed across fencing epoch: %q != %q", first, second)
	}

	eventID, err := NewOperationID(next, OperationEvent, 7)
	if err != nil {
		t.Fatalf("NewOperationID(event) error = %v", err)
	}
	if eventID == second {
		t.Fatal("event and effect IDs must have separate identity domains")
	}
}

func TestAdvanceEpochPreservesLogicalActivationAndRejectsOverflow(t *testing.T) {
	activation := Activation{RunID: "run-1", ID: "activation-1", Epoch: 1}
	next, err := activation.AdvanceEpoch()
	if err != nil {
		t.Fatalf("AdvanceEpoch() error = %v", err)
	}

	if next.RunID != activation.RunID || next.ID != activation.ID {
		t.Fatalf("AdvanceEpoch() changed logical activation: got %#v want run=%q id=%q", next, activation.RunID, activation.ID)
	}
	if next.Epoch != 2 {
		t.Fatalf("AdvanceEpoch().Epoch = %d, want 2", next.Epoch)
	}

	_, err = (Activation{RunID: "run-1", ID: "activation-1", Epoch: Epoch(math.MaxUint64)}).AdvanceEpoch()
	if !errors.Is(err, ErrEpochExhausted) {
		t.Fatalf("AdvanceEpoch() overflow error = %v, want %v", err, ErrEpochExhausted)
	}
}

func TestManifestDelegationCannotManufactureAuthority(t *testing.T) {
	mergeMain := Grant{
		Operation: "forge.merge",
		Resource:  "repo:dporkka/dev-plane/pr:123",
		Revision:  "abc123",
	}
	readSecret := Grant{
		Operation: "secret.read",
		Resource:  "secret:github-app-token",
	}

	parent, err := NewManifest(mergeMain, readSecret)
	if err != nil {
		t.Fatalf("NewManifest(parent) error = %v", err)
	}
	child, err := NewManifest(mergeMain)
	if err != nil {
		t.Fatalf("NewManifest(child) error = %v", err)
	}
	delegated, err := parent.Delegate(child)
	if err != nil {
		t.Fatalf("Delegate(valid subset) error = %v", err)
	}

	allowed, err := delegated.Allows(mergeMain)
	if err != nil {
		t.Fatalf("Allows() error = %v", err)
	}
	if !allowed {
		t.Fatal("delegated manifest should retain exact requested grant")
	}

	escalation, err := NewManifest(Grant{
		Operation: "forge.merge",
		Resource:  "repo:dporkka/dev-plane/pr:999",
		Revision:  "def456",
	})
	if err != nil {
		t.Fatalf("NewManifest(escalation) error = %v", err)
	}
	_, err = parent.Delegate(escalation)
	if !errors.Is(err, ErrAuthorityEscalation) {
		t.Fatalf("Delegate(escalation) error = %v, want %v", err, ErrAuthorityEscalation)
	}
}

func TestMalformedManifestFailsClosedEvenWhenRequestedGrantIsPresent(t *testing.T) {
	valid := Grant{Operation: "secret.read", Resource: "secret:github-app-token"}
	manifest := Manifest{
		Grants: []Grant{
			valid,
			{Operation: "", Resource: "malformed"},
		},
	}

	allowed, err := manifest.Allows(valid)
	if err == nil {
		t.Fatal("Allows() error = nil, want malformed manifest rejection")
	}
	if allowed {
		t.Fatal("malformed manifest must not partially authorize a valid sibling grant")
	}
}

func TestEffectIntentRetainsStableIdentityAcrossTakeoverButFencesStaleOwner(t *testing.T) {
	grant := Grant{
		Operation: "forge.merge",
		Resource:  "repo:dporkka/dev-plane/pr:123",
		Revision:  "abc123",
	}
	activation := Activation{RunID: "run-1", ID: "activation-1", Epoch: 8}

	intent, err := NewEffectIntent(activation, 3, grant, []byte("merge authorized head abc123"))
	if err != nil {
		t.Fatalf("NewEffectIntent() error = %v", err)
	}

	current, err := activation.AdvanceEpoch()
	if err != nil {
		t.Fatalf("AdvanceEpoch() error = %v", err)
	}
	recovered, err := NewEffectIntent(current, 3, grant, []byte("merge authorized head abc123"))
	if err != nil {
		t.Fatalf("NewEffectIntent() after takeover error = %v", err)
	}

	if recovered.ID != intent.ID {
		t.Fatalf("effect identity changed across takeover: %q != %q", recovered.ID, intent.ID)
	}
	if err := intent.AuthorizeAgainst(current); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale intent authorization error = %v, want %v", err, ErrStaleEpoch)
	}
	if err := recovered.AuthorizeAgainst(current); err != nil {
		t.Fatalf("current intent AuthorizeAgainst() error = %v", err)
	}
}

func TestRecoverEffectReplaysMatchingReceiptAndRejectsConflicts(t *testing.T) {
	activation := Activation{RunID: "run-1", ID: "activation-1", Epoch: 1}
	grant := Grant{Operation: "forge.merge", Resource: "repo:dporkka/dev-plane/pr:123", Revision: "abc123"}
	intent, err := NewEffectIntent(activation, 0, grant, []byte("request"))
	if err != nil {
		t.Fatalf("NewEffectIntent() error = %v", err)
	}

	action, err := RecoverEffect(intent, nil)
	if err != nil {
		t.Fatalf("RecoverEffect(no receipt) error = %v", err)
	}
	if action != EffectExecute {
		t.Fatalf("RecoverEffect(no receipt) = %q, want %q", action, EffectExecute)
	}

	receipt, err := NewEffectReceipt(intent, "github", "merge-sha-1", []byte("merged"))
	if err != nil {
		t.Fatalf("NewEffectReceipt() error = %v", err)
	}
	action, err = RecoverEffect(intent, &receipt)
	if err != nil {
		t.Fatalf("RecoverEffect(receipt) error = %v", err)
	}
	if action != EffectReplayRecordedResult {
		t.Fatalf("RecoverEffect(receipt) = %q, want %q", action, EffectReplayRecordedResult)
	}

	conflict := receipt
	conflict.Reference = "different-merge-sha"
	_, err = MergeReceipt(receipt, conflict)
	if !errors.Is(err, ErrReceiptConflict) {
		t.Fatalf("MergeReceipt(conflict) error = %v, want %v", err, ErrReceiptConflict)
	}

	same, err := MergeReceipt(receipt, receipt)
	if err != nil {
		t.Fatalf("MergeReceipt(idempotent) error = %v", err)
	}
	if same != receipt {
		t.Fatalf("MergeReceipt(idempotent) = %#v, want %#v", same, receipt)
	}
}


func TestMergeIntentIsIdempotentButRejectsChangedPayloadForSameOperation(t *testing.T) {
	activation := Activation{RunID: "run-1", ID: "activation-1", Epoch: 1}
	grant := Grant{Operation: "forge.merge", Resource: "repo:dporkka/dev-plane/pr:123", Revision: "abc123"}

	first, err := NewEffectIntent(activation, 5, grant, []byte("authorized payload"))
	if err != nil {
		t.Fatalf("NewEffectIntent(first) error = %v", err)
	}
	same, err := NewEffectIntent(activation, 5, grant, []byte("authorized payload"))
	if err != nil {
		t.Fatalf("NewEffectIntent(same) error = %v", err)
	}
	merged, err := MergeIntent(first, same)
	if err != nil {
		t.Fatalf("MergeIntent(idempotent) error = %v", err)
	}
	if merged != first {
		t.Fatalf("MergeIntent(idempotent) = %#v, want %#v", merged, first)
	}

	changed, err := NewEffectIntent(activation, 5, grant, []byte("different payload"))
	if err != nil {
		t.Fatalf("NewEffectIntent(changed) error = %v", err)
	}
	if changed.ID != first.ID {
		t.Fatal("same logical ordinal must retain one operation ID so payload drift is detectable")
	}
	_, err = MergeIntent(first, changed)
	if !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("MergeIntent(changed payload) error = %v, want %v", err, ErrIntentConflict)
	}
}
