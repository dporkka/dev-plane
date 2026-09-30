package gateway

import (
	"strings"

	"github.com/ai-dev-control-plane/vcs"
)

// NewGitHubBranchPublisher wraps a source-control publisher with GitHub's
// HTTPS token convention. The GitHub-specific x-access-token username remains
// confined to this adapter rather than leaking into PR orchestration.
func NewGitHubBranchPublisher(inner vcs.Publisher, token string) vcs.Publisher {
	if inner == nil {
		inner = vcs.NewGitBackend(nil)
	}
	return vcs.NewHTTPBasicPublisher(inner, "x-access-token", strings.TrimSpace(token))
}
