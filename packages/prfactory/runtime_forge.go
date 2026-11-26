package prfactory

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ai-dev-control-plane/gateway"
)

// ErrForgeConfiguration means the operator's selected forge configuration cannot
// safely be used. No repository mutation should be attempted in this state.
var ErrForgeConfiguration = errors.New("invalid forge runtime configuration")

// configureGiteaRuntime deliberately does not reuse either a GitHub token or
// the Gitea API token as a Git password. The operator must configure the HTTPS
// credential pair explicitly, or leave both unset for SSH transport.
func (f *Factory) configureGiteaRuntime() error {
	forge, err := gateway.NewGiteaForge(os.Getenv("GITEA_URL"), nil)
	if err != nil {
		return fmt.Errorf("%w: GITEA_URL must identify a valid forge instance", ErrForgeConfiguration)
	}
	token := os.Getenv("GITEA_TOKEN")
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return fmt.Errorf("%w: GITEA_TOKEN is required and must be a valid token", ErrForgeConfiguration)
	}
	username, password := os.Getenv("GITEA_GIT_USERNAME"), os.Getenv("GITEA_GIT_PASSWORD")
	if (username == "") != (password == "") || strings.TrimSpace(username) != username || strings.TrimSpace(password) != password || strings.ContainsAny(username+password, "\r\n\x00") {
		return fmt.Errorf("%w: GITEA_GIT_USERNAME and GITEA_GIT_PASSWORD must be a valid pair or both unset", ErrForgeConfiguration)
	}
	f.forge = forge
	f.forgeCredential = gateway.ForgeCredential{AccessToken: token}
	f.gitPushCredential = GitPushCredential{Username: username, Password: password}
	return nil
}
