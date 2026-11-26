package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGiteaWebhookRejectsTruncatedSignedPrefix(t *testing.T) {
	const bodyLimit = 10 * 1024 * 1024
	const secret = "synthetic-secret"
	// Construct a valid signed JSON payload exactly at the size limit,
	// then append a byte that would previously be discarded before HMAC.
	body := make([]byte, bodyLimit+1)
	copy(body, []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`))
	for i := len(`{"repository":{"id":77,"full_name":"acme/widget"}}`); i < bodyLimit; i++ {
		body[i] = ' '
	}
	body[bodyLimit] = 'x'
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/gitea", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Delivery", "delivery-size")
	req.Header.Set("X-Gitea-Signature", ComputeGiteaWebhookSignature(body[:bodyLimit], secret))
	if _, err := ParseGiteaWebhook(req, secret); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected explicit size refusal, got %v", err)
	}
}
