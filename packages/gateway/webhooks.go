package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHub webhook event type constants.
const (
	GitHubEventPush         = "push"
	GitHubEventPullRequest  = "pull_request"
	GitHubEventIssues       = "issues"
	GitHubEventIssueComment = "issue_comment"
	GitHubEventCreate       = "create"
	GitHubEventDelete       = "delete"
	GitHubEventPing         = "ping"
)

// WebhookEvent represents a parsed incoming webhook event.
type WebhookEvent struct {
	Source       string          `json:"source"`
	EventType    string          `json:"event_type"`
	DeliveryID   string          `json:"delivery_id"`
	RepositoryID int64           `json:"repository_id,omitempty"`
	Repository   string          `json:"repository,omitempty"`
	Sender       string          `json:"sender,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	ReceivedAt   time.Time       `json:"received_at"`
	Signature    string          `json:"signature,omitempty"`
}

// ValidateGitHubWebhook verifies that a webhook payload's signature matches
// the expected HMAC-SHA256 signature computed with the shared secret.
func ValidateGitHubWebhook(payload []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}

	// GitHub signatures are prefixed with "sha256="
	expectedSig := "sha256=" + computeHMACSHA256(payload, secret)
	return hmac.Equal([]byte(expectedSig), []byte(signature))
}

// ComputeGitHubWebhookSignature computes the HMAC-SHA256 signature for a payload.
// Useful for testing webhook handlers.
func ComputeGitHubWebhookSignature(payload []byte, secret string) string {
	return "sha256=" + computeHMACSHA256(payload, secret)
}

func computeHMACSHA256(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// ParseGitHubWebhook reads and parses a GitHub webhook request.
func ParseGitHubWebhook(r *http.Request, secret string) (*WebhookEvent, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return nil, fmt.Errorf("read webhook body: %w", err)
	}
	defer r.Body.Close()

	// Validate signature if secret is provided
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if sig == "" {
			return nil, fmt.Errorf("missing X-Hub-Signature-256 header")
		}
		if !ValidateGitHubWebhook(body, sig, secret) {
			return nil, fmt.Errorf("invalid webhook signature")
		}
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		return nil, fmt.Errorf("missing X-GitHub-Event header")
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")

	// Extract repository info from payload
	var payloadMeta struct {
		Repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	_ = json.Unmarshal(body, &payloadMeta)

	return &WebhookEvent{
		Source:       "github",
		EventType:    eventType,
		DeliveryID:   deliveryID,
		RepositoryID: payloadMeta.Repository.ID,
		Repository:   payloadMeta.Repository.FullName,
		Sender:       payloadMeta.Sender.Login,
		Payload:      body,
		ReceivedAt:   time.Now(),
		Signature:    r.Header.Get("X-Hub-Signature-256"),
	}, nil
}

// IsBranchPush returns true if the webhook event is a push to a branch (not a tag).
func IsBranchPush(event *WebhookEvent) (bool, string) {
	if event.EventType != GitHubEventPush {
		return false, ""
	}

	var payload struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return false, ""
	}

	if strings.HasPrefix(payload.Ref, "refs/heads/") {
		return true, strings.TrimPrefix(payload.Ref, "refs/heads/")
	}
	return false, ""
}

// ExtractIssueNumber tries to extract an issue number from a webhook payload.
func ExtractIssueNumber(event *WebhookEvent) int {
	if event.EventType != GitHubEventIssues && event.EventType != GitHubEventIssueComment {
		return 0
	}

	var payload struct {
		Issue struct {
			Number int `json:"number"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return 0
	}
	return payload.Issue.Number
}

// WebhookResponse is the standard response sent back to webhook callers.
type WebhookResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// RespondWebhook writes a standard webhook response.
func RespondWebhook(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(WebhookResponse{
		Status:  http.StatusText(status),
		Message: msg,
	})
}
