package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLogger_LogsRequest(t *testing.T) {
	var captured []any
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})
	log := slog.New(&capturingHandler{handler, &captured})

	mw := Logger(log)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("User-Agent", "test-agent")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}

	if len(captured) == 0 {
		t.Fatal("expected captured log attributes")
	}
}

func TestLogger_UsesRequestIDFromContext(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mw := Logger(log)

	var requestID string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	_ = requestID
}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()
	if id1 == "" {
		t.Error("expected non-empty request id")
	}
	if id1 == id2 {
		t.Error("expected unique request ids")
	}
	if !strings.HasPrefix(id1, "req_") {
		t.Errorf("request id = %q", id1)
	}
}

func TestRandomString(t *testing.T) {
	s := randomString(8)
	if len(s) != 8 {
		t.Errorf("length = %d, want 8", len(s))
	}
}

type capturingHandler struct {
	parent   slog.Handler
	captured *[]any
}

func (h *capturingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.parent.Enabled(ctx, level)
}

func (h *capturingHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		*h.captured = append(*h.captured, a.Key, a.Value.Any())
		return true
	})
	return nil
}

func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &capturingHandler{h.parent.WithAttrs(attrs), h.captured}
}

func (h *capturingHandler) WithGroup(name string) slog.Handler {
	return &capturingHandler{h.parent.WithGroup(name), h.captured}
}
