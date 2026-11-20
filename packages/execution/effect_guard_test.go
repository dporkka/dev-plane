package execution

import (
	"context"
	"errors"
	"testing"
)

func TestCheckEffectGuardFailsClosedAfterOwnershipLoss(t *testing.T) {
	calls := 0
	guard := errors.New("stale worker ownership")
	ctx := WithEffectGuard(context.Background(), func(context.Context) error {
		calls++
		return guard
	})
	if err := CheckEffectGuard(ctx); !errors.Is(err, guard) {
		t.Fatalf("stale worker must not run effect: %v", err)
	}
	if calls != 1 {
		t.Fatalf("guard calls=%d, want 1", calls)
	}
}

func TestCheckEffectGuardPreservesOrdinaryAndCancelledContexts(t *testing.T) {
	if err := CheckEffectGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckEffectGuard(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context must reject effect: %v", err)
	}
}
