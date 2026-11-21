package execution

import (
	"context"
	"errors"
)

// ErrRunLeaseUnavailable rejects a write or claim without current run ownership.
var ErrRunLeaseUnavailable = errors.New("run execution lease unavailable")

type runStatusLeaseKey struct{}

type runStatusLease struct {
	runID string
	token string
}

// WithRunStatusLease attaches the claimed run and token for conditional database writes.
func WithRunStatusLease(ctx context.Context, runID, token string) context.Context {
	return context.WithValue(ctx, runStatusLeaseKey{}, runStatusLease{runID: runID, token: token})
}

// RunStatusLeaseToken returns the bound token for a conditional write. An empty
// token with no error is reserved for contexts that have no lease or effect guard.
func RunStatusLeaseToken(ctx context.Context, runID string) (string, error) {
	if ctx != nil {
		if lease, ok := ctx.Value(runStatusLeaseKey{}).(runStatusLease); ok {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if lease.runID == "" || lease.runID != runID || lease.token == "" {
				return "", ErrRunLeaseUnavailable
			}
			return lease.token, nil
		}
	}
	if err := CheckEffectGuard(ctx); err != nil {
		return "", err
	}
	if _, guarded := ctx.Value(effectGuardKey{}).(EffectGuard); guarded {
		return "", ErrRunLeaseUnavailable
	}
	return "", nil
}
