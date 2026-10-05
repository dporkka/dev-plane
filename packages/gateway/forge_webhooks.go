package gateway

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnsupportedForgeProvider is returned when webhook ingress is asked to
// interpret a provider whose delivery semantics are not implemented.
var ErrUnsupportedForgeProvider = errors.New("unsupported forge provider")

// ValidateGiteaWebhook validates Gitea's native X-Gitea-Signature header. The
// header is the lowercase hexadecimal HMAC-SHA256 digest without a prefix.
func ValidateGiteaWebhook(payload []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}
	expected := computeHMACSHA256(payload, secret)
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(strings.TrimSpace(signature))))
}

// ComputeGiteaWebhookSignature computes the native Gitea HMAC-SHA256 signature.
func ComputeGiteaWebhookSignature(payload []byte, secret string) string {
	return computeHMACSHA256(payload, secret)
}

// ParseGiteaWebhook verifies and normalizes a Gitea webhook into the same
// WebhookEvent shape used by GitHub ingress. Gitea's native signature is
// preferred; the documented X-Hub-Signature-256 compatibility header is
// accepted as a fallback.
func ParseGiteaWebhook(r *http.Request, secret string) (*WebhookEvent, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read webhook body: %w", err)
	}
	defer r.Body.Close()

	signature := strings.TrimSpace(r.Header.Get("X-Gitea-Signature"))
	if secret != "" {
		switch {
		case signature != "":
			if !ValidateGiteaWebhook(body, signature, secret) {
				return nil, fmt.Errorf("invalid gitea webhook signature")
			}
		case strings.TrimSpace(r.Header.Get("X-Hub-Signature-256")) != "":
			hubSignature := r.Header.Get("X-Hub-Signature-256")
			if !ValidateGitHubWebhook(body, hubSignature, secret) {
				return nil, fmt.Errorf("invalid gitea webhook signature")
			}
			signature = hubSignature
		default:
			return nil, fmt.Errorf("missing X-Gitea-Signature or X-Hub-Signature-256 header")
		}
	}

	eventType := strings.TrimSpace(r.Header.Get("X-Gitea-Event-Type"))
	if eventType == "" {
		eventType = strings.TrimSpace(r.Header.Get("X-Gitea-Event"))
	}
	if eventType == "" {
		return nil, fmt.Errorf("missing X-Gitea-Event header")
	}

	var payloadMeta struct {
		Repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(body, &payloadMeta); err != nil {
		return nil, fmt.Errorf("decode gitea webhook payload: %w", err)
	}

	return &WebhookEvent{
		Source:       "gitea",
		EventType:    eventType,
		DeliveryID:   strings.TrimSpace(r.Header.Get("X-Gitea-Delivery")),
		RepositoryID: payloadMeta.Repository.ID,
		Repository:   payloadMeta.Repository.FullName,
		Sender:       payloadMeta.Sender.Login,
		Payload:      body,
		ReceivedAt:   time.Now(),
		Signature:    signature,
	}, nil
}

// ParseForgeWebhook dispatches webhook parsing through the provider-specific
// verifier while returning one canonical WebhookEvent representation.
func ParseForgeWebhook(provider string, r *http.Request, secret string) (*WebhookEvent, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github", "":
		return ParseGitHubWebhook(r, secret)
	case "gitea":
		return ParseGiteaWebhook(r, secret)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedForgeProvider, provider)
	}
}
