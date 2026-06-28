// Package respond provides HTTP response helpers for JSON and SSE.
package respond

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// JSON writes a JSON response with the given status code and data.
// It marshals the payload before writing headers so that encoding errors can
// be reported to the caller instead of producing a partial response.
func JSON(w http.ResponseWriter, status int, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		slog.Default().Error("failed to marshal JSON response", "error", err)
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		slog.Default().Error("failed to write JSON response", "error", err)
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
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			// SSE data fields may contain newlines; prefix each line.
			for _, line := range strings.Split(event, "\n") {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}
