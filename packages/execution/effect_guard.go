package execution

import (
	"context"
	"errors"
)

// EffectGuard checks a worker's current execution ownership immediately
// before an agent tool is dispatched. It is an advisory preflight, not
// transactional fencing of an already-started external side effect.
type EffectGuard func(context.Context) error

type effectGuardKey struct{}

// WithEffectGuard attaches an optional preflight check to the execution
// context. Existing code without a guard retains its previous behavior.
func WithEffectGuard(ctx context.Context, guard EffectGuard) context.Context {
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, effectGuardKey{}, guard)
}

// CheckEffectGuard denies tool dispatch on cancellation or a stale owner.
// The caller must still pass ctx to the tool for cooperative cancellation.
func CheckEffectGuard(ctx context.Context) error {
	if ctx == nil {
		return errors.New("effect execution context unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if guard, ok := ctx.Value(effectGuardKey{}).(EffectGuard); ok && guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}
