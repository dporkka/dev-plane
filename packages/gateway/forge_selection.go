package gateway

import (
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/vcs"
)

// ForgeSettings describes application-level selection of one built-in forge
// adapter. Provider-neutral orchestration consumes the resulting interfaces.
type ForgeSettings struct {
	Provider              string
	GitRemote             string
	GitHubClientID        string
	GitHubClientSecret    string
	GitHubToken           string
	GiteaURL              string
	GiteaToken            string
	GiteaUsername         string
	GiteaDraftTitlePrefix string
}

// ForgeSelection is the composed review API + branch publication boundary for
// one configured forge.
type ForgeSelection struct {
	Provider   forge.Provider
	Credential forge.Credential
	Publisher  vcs.Publisher
	Remote     string
}

// SelectForge builds one of Dev Plane's built-in forge adapters.
//
// A missing GitHub token preserves the historical behavior of leaving the
// default GitHub integration disabled. Gitea/Forgejo may still be selected
// without a token so deployments using ambient API/Git authentication remain
// possible.
func SelectForge(settings ForgeSettings) (*ForgeSelection, error) {
	providerName := strings.ToLower(strings.TrimSpace(settings.Provider))
	if providerName == "" {
		providerName = "github"
	}
	remote := strings.TrimSpace(settings.GitRemote)
	if remote == "" {
		remote = "origin"
	}

	basePublisher := vcs.Publisher(vcs.NewGitBackend(nil))

	switch providerName {
	case "github":
		token := strings.TrimSpace(settings.GitHubToken)
		if token == "" {
			return nil, nil
		}
		provider := NewGitHubGateway(settings.GitHubClientID, settings.GitHubClientSecret)
		return &ForgeSelection{
			Provider:   provider,
			Credential: forge.Credential{Token: token},
			Publisher:  NewGitHubBranchPublisher(basePublisher, token),
			Remote:     remote,
		}, nil

	case "gitea", "forgejo":
		if strings.TrimSpace(settings.GiteaURL) == "" {
			return nil, fmt.Errorf("%s forge provider requires GITEA_URL", providerName)
		}
		provider := NewGiteaGateway(settings.GiteaURL)
		if prefix := strings.TrimSpace(settings.GiteaDraftTitlePrefix); prefix != "" {
			provider.WithDraftTitlePrefix(prefix)
		}

		token := strings.TrimSpace(settings.GiteaToken)
		username := strings.TrimSpace(settings.GiteaUsername)
		publisher := basePublisher
		if username != "" && token != "" {
			publisher = NewGiteaBranchPublisher(basePublisher, username, token)
		}

		return &ForgeSelection{
			Provider:   provider,
			Credential: forge.Credential{Token: token},
			Publisher:  publisher,
			Remote:     remote,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported forge provider %q", providerName)
	}
}
