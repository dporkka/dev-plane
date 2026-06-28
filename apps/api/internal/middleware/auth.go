package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// Auth returns a middleware that validates JWT Bearer tokens and injects user claims into the request context.
func Auth(jwtSecret string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractBearer(r)
			if tokenStr == "" {
				respond.Error(w, http.StatusUnauthorized, errMissingToken)
				return
			}

			claims, err := auth.ValidateToken(tokenStr, jwtSecret)
			if err != nil {
				logger.WarnContext(r.Context(), "invalid jwt token",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
				)
				respond.Error(w, http.StatusUnauthorized, errInvalidToken)
				return
			}

			ctx := auth.WithUser(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

var (
	errMissingToken = &authError{msg: "unauthorized: missing token"}
	errInvalidToken = &authError{msg: "invalid token"}
)

type authError struct {
	msg string
}

func (e *authError) Error() string {
	return e.msg
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
