package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGiteaWebhookInstanceURL(t *testing.T) {
	root, err := GiteaWebhookInstanceURL(" https://GIT.example.test/code/ ")
	if err != nil || root != "https://git.example.test/code" {
		t.Fatalf("unexpected root %q, err=%v", root, err)
	}
	for _, raw := range []string{
		"", "https://git.example.test/code/api/v1", "https://git.example.test/code/../other",
		"https://git.example.test/code//other", "https://git.example.test/code%2fother",
		"https://user:secret@git.example.test", "https://git.example.test?credential=secret",
		"https://git.example.test/code#fragment", "file:///tmp/forge",
	} {
		if root, err := GiteaWebhookInstanceURL(raw); err == nil || root != "" {
			t.Fatalf("accepted invalid instance %q", raw)
		}
	}
}

func TestGiteaWebhookReceiptIdentity(t *testing.T) {
	const instance = "https://git.example.test/code"
	for _, tc := range []struct {
		name, htmlURL, fullName, envelope, delivery, eventType string
		id                                                     int64
		want                                                   bool
	}{
		{"valid", instance + "/acme/widget", "acme/widget", "acme/widget", "delivery-1", "issues", 77, true},
		{"other host", "https://other.example.test/code/acme/widget", "acme/widget", "acme/widget", "d", "issues", 77, false},
		{"other subpath", "https://git.example.test/other/acme/widget", "acme/widget", "acme/widget", "d", "issues", 77, false},
		{"other port", "https://git.example.test:8443/code/acme/widget", "acme/widget", "acme/widget", "d", "issues", 77, false},
		{"other repository", instance + "/acme/other", "acme/widget", "acme/widget", "d", "issues", 77, false},
		{"changed envelope repository", instance + "/acme/widget", "acme/widget", "acme/other", "d", "issues", 77, false},
		{"missing ID", instance + "/acme/widget", "acme/widget", "acme/widget", "d", "issues", 0, false},
		{"traversal", instance + "/acme/..", "acme/..", "acme/..", "d", "issues", 77, false},
		{"escaped path", instance + "/acme/%2e%2e", "acme/%2e%2e", "acme/%2e%2e", "d", "issues", 77, false},
		{"header too long", instance + "/acme/widget", "acme/widget", "acme/widget", strings.Repeat("d", 129), "issues", 77, false},
		{"malformed event type", instance + "/acme/widget", "acme/widget", "acme/widget", "d", "issues\nmerge", 77, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"repository": map[string]any{"id": tc.id, "full_name": tc.fullName, "html_url": tc.htmlURL}})
			identity, err := GiteaWebhookReceiptIdentity(instance, tc.delivery, tc.eventType, tc.envelope, body)
			if (err == nil) != tc.want {
				t.Fatalf("result = %+v, err=%v", identity, err)
			}
			if tc.want && (identity.ID != tc.id || identity.FullName != tc.fullName) {
				t.Fatalf("wrong signed repository identity: %+v", identity)
			}
		})
	}
}
