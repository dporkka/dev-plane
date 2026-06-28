package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5/middleware"
)

// Recovery returns a panic recovery middleware that logs the stack trace with
// the provided logger and returns a JSON 500 error.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := debug.Stack()
					logger.ErrorContext(r.Context(), "panic recovered",
						"error", rec,
						"stack", string(stack),
						"method", r.Method,
						"path", r.URL.Path,
						"request_id", middleware.GetReqID(r.Context()),
					)

					payload, _ := json.Marshal(map[string]string{
						"error": "internal server error",
					})
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write(payload)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
