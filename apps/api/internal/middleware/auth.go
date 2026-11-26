package middleware

import (
	"database/sql"
	"errors"
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

// AuthWithMembership validates the JWT and then reloads the user's current
// organization membership. A signed JWT is proof of authentication, not proof
// that its embedded role remains authorized after a user is demoted or removed.
func AuthWithMembership(jwtSecret string, logger *slog.Logger, db *sql.DB) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		verified := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if db == nil {
				respond.Error(w, http.StatusServiceUnavailable, errMembershipUnavailable)
				return
			}
			claims := auth.UserFromContext(r.Context())
			if claims == nil || claims.UserID == "" || claims.OrgID == "" {
				respond.Error(w, http.StatusUnauthorized, errInvalidToken)
				return
			}

			var role string
			err := db.QueryRowContext(r.Context(), `
				SELECT role FROM users
				WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
			`, claims.UserID, claims.OrgID).Scan(&role)
			if errors.Is(err, sql.ErrNoRows) {
				respond.Error(w, http.StatusUnauthorized, errInvalidToken)
				return
			}
			if err != nil {
				logger.ErrorContext(r.Context(), "could not verify current membership", "error", err)
				respond.Error(w, http.StatusServiceUnavailable, errMembershipUnavailable)
				return
			}
			switch role {
			case "owner", "admin", "member":
			default:
				respond.Error(w, http.StatusUnauthorized, errInvalidToken)
				return
			}
			fresh := *claims
			fresh.Role = role
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), &fresh)))
		})
		return Auth(jwtSecret, logger)(verified)
	}
}

var errMembershipUnavailable = &authError{msg: "membership verification unavailable"}
