package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
)

// Recovery is a panic recovery middleware that logs the stack trace and returns a 500 JSON error.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				// Log the panic and stack trace
				fmt.Printf("[PANIC] %v\n%s\n", rec, stack)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
