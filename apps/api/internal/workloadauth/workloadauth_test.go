package workloadauth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifierAcceptsFreshSignedRequest(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	verifier, err := NewVerifier("nulang-cloud", "0123456789abcdef0123456789abcdef", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier.Now = func() time.Time { return now }

	body := []byte(`{"task_id":"task-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/authorize", bytes.NewReader(body))
	Sign(req, "nulang-cloud", "0123456789abcdef0123456789abcdef", now, body)

	if err := verifier.Verify(req, body); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
}

func TestVerifierRejectsTamperedBody(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	verifier, _ := NewVerifier("nulang-cloud", "0123456789abcdef0123456789abcdef", time.Minute)
	verifier.Now = func() time.Time { return now }

	original := []byte(`{"task_id":"task-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/authorize", bytes.NewReader(original))
	Sign(req, "nulang-cloud", "0123456789abcdef0123456789abcdef", now, original)

	if err := verifier.Verify(req, []byte(`{"task_id":"task-2"}`)); err == nil {
		t.Fatal("tampered body should fail verification")
	}
}

func TestVerifierRejectsWrongWorkloadAndStaleTimestamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	secret := "0123456789abcdef0123456789abcdef"
	verifier, _ := NewVerifier("nulang-cloud", secret, time.Minute)
	verifier.Now = func() time.Time { return now }
	body := []byte("{}")

	t.Run("wrong workload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/authorize", bytes.NewReader(body))
		Sign(req, "other-workload", secret, now, body)
		if err := verifier.Verify(req, body); err == nil {
			t.Fatal("wrong workload should fail verification")
		}
	})

	t.Run("stale timestamp", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/authorize", bytes.NewReader(body))
		Sign(req, "nulang-cloud", secret, now.Add(-2*time.Minute), body)
		if err := verifier.Verify(req, body); err == nil {
			t.Fatal("stale timestamp should fail verification")
		}
	})
}

func TestNewVerifierRejectsWeakConfiguration(t *testing.T) {
	if _, err := NewVerifier("", "0123456789abcdef0123456789abcdef", time.Minute); err == nil {
		t.Fatal("empty workload should be rejected")
	}
	if _, err := NewVerifier("nulang-cloud", "short", time.Minute); err == nil {
		t.Fatal("short secret should be rejected")
	}
	if _, err := NewVerifier("nulang-cloud", "0123456789abcdef0123456789abcdef", 0); err == nil {
		t.Fatal("non-positive skew should be rejected")
	}
}
