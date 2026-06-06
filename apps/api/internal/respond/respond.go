// Package respond provides HTTP response helpers for JSON and SSE.
package respond

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// JSON writes a JSON response with the given status code and data.
func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Fallback if encoding fails
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// Error writes a JSON error response with the given status code and error message.
func Error(w http.ResponseWriter, status int, err error) {
	JSON(w, status, map[string]string{"error": err.Error()})
}

// SSE streams server-sent events to the client.
// The events channel should be closed by the caller when done.
func SSE(w http.ResponseWriter, r *http.Request, events <-chan string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	for {
		select {
		case event, open := <-events:
			if !open {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", event)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
