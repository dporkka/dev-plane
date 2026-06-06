package auth

import (
	"context"
)

// contextKey is a private type to avoid collisions with other context keys.
type contextKey string

const userContextKey contextKey = "user"

// UserFromContext extracts user claims from the context.
// Returns nil if no user is present in the context.
func UserFromContext(ctx context.Context) *Claims {
	v := ctx.Value(userContextKey)
	if v == nil {
		return nil
	}
	if claims, ok := v.(*Claims); ok {
		return claims
	}
	return nil
}

// WithUser injects user claims into the context.
func WithUser(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, userContextKey, claims)
}
