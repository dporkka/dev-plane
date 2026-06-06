// Package middleware provides HTTP middleware for the API service.
package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"log/slog"
)

// Logger returns a Chi-compatible HTTP request logger using slog.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			requestID := middleware.GetReqID(r.Context())
			if requestID == "" {
				requestID = generateRequestID()
			}

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				duration := time.Since(start)
				log.Info("http request",
					"request_id", requestID,
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"duration_ms", duration.Milliseconds(),
					"bytes", ww.BytesWritten(),
					"remote_addr", r.RemoteAddr,
					"user_agent", r.UserAgent(),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func generateRequestID() string {
	return "req_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
