package gateway

import (
	"strings"

	"github.com/ai-dev-control-plane/vcs"
)

// NewGiteaBranchPublisher wraps a source-control publisher with the standard
// Gitea/Forgejo HTTPS username + access-token-as-password convention.
func NewGiteaBranchPublisher(inner vcs.Publisher, username, token string) vcs.Publisher {
	if inner == nil {
		inner = vcs.NewGitBackend(nil)
	}
	return vcs.NewHTTPBasicPublisher(inner, strings.TrimSpace(username), strings.TrimSpace(token))
}
