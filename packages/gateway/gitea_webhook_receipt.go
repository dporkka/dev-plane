package gateway

import (
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
)

var errInvalidGiteaReceipt = errors.New("invalid gitea webhook receipt identity")

// GiteaWebhookInstanceURL validates an operator-controlled instance root. Unlike
// the API adapter, ingress deliberately rejects API URLs and ambiguous paths.
func GiteaWebhookInstanceURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errInvalidGiteaReceipt
	}
	rootPath := strings.TrimRight(parsed.Path, "/")
	if parsed.RawPath != "" || strings.HasSuffix(rootPath, "/api/v1") ||
		(rootPath != "" && path.Clean(rootPath) != rootPath) ||
		strings.Contains(parsed.Path, "//") || strings.Contains(parsed.Path, "\\") {
		return "", errInvalidGiteaReceipt
	}
	return canonicalGiteaInstanceURL(raw)
}

// GiteaReceiptRepository is the repository identity inside a signed body.
// It is external metadata, never a Dev Plane tenant/repository authorization.
type GiteaReceiptRepository struct {
	ID       int64
	FullName string
}

// GiteaWebhookReceiptIdentity checks the signed repository URL against the
// configured instance and the signed repository name. Delivery and event-type
// headers are bounded but are not authenticated by Gitea's body HMAC; they must
// never authorize an action.
func GiteaWebhookReceiptIdentity(instance, deliveryID, eventType, repository string, body []byte) (GiteaReceiptRepository, error) {
	if !validReceiptHeader(deliveryID) || !validReceiptHeader(eventType) || len(body) == 0 || len(body) > maxGiteaWebhookBytes {
		return GiteaReceiptRepository{}, errInvalidGiteaReceipt
	}
	var payload struct {
		Repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
			HTMLURL  string `json:"html_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return GiteaReceiptRepository{}, errInvalidGiteaReceipt
	}
	repo := payload.Repository
	parts := strings.Split(repo.FullName, "/")
	if repo.ID <= 0 || repo.FullName != repository || len(repo.FullName) > 512 || len(parts) != 2 {
		return GiteaReceiptRepository{}, errInvalidGiteaReceipt
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || url.PathEscape(part) != part || strings.ContainsAny(part, "\\:") {
			return GiteaReceiptRepository{}, errInvalidGiteaReceipt
		}
	}
	if repo.HTMLURL != instance+"/"+repo.FullName {
		return GiteaReceiptRepository{}, errInvalidGiteaReceipt
	}
	return GiteaReceiptRepository{ID: repo.ID, FullName: repo.FullName}, nil
}

func validReceiptHeader(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}
