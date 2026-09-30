package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ai-dev-control-plane/api/internal/config"
	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/ai-dev-control-plane/vcs"
)

type forgeIntegration struct {
	provider   forge.Provider
	credential forge.Credential
	creator    *prfactory.Factory
}

func buildForgeIntegration(cfg *config.Config, db *sql.DB, logger *slog.Logger) (*forgeIntegration, error) {
	if cfg == nil {
		return nil, fmt.Errorf("forge configuration is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	providerName := strings.ToLower(strings.TrimSpace(cfg.ForgeProvider))
	if providerName == "" {
		providerName = "github"
	}
	remote := strings.TrimSpace(cfg.ForgeGitRemote)
	if remote == "" {
		remote = "origin"
	}

	basePublisher := vcs.Publisher(vcs.NewGitBackend(nil))

	switch providerName {
	case "github":
		token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
		if token == "" {
			return nil, nil
		}
		provider := gateway.NewGitHubGateway(cfg.GitHubClientID, cfg.GitHubSecret)
		publisher := gateway.NewGitHubBranchPublisher(basePublisher, token)
		creator := prfactory.NewFactory(db, logger).
			WithForgeProvider(provider).
			WithForgeCredential(token).
			WithBranchPublisher(publisher).
			WithBranchRemote(remote)
		return &forgeIntegration{
			provider:   provider,
			credential: forge.Credential{Token: token},
			creator:    creator,
		}, nil

	case "gitea", "forgejo":
		if strings.TrimSpace(cfg.GiteaURL) == "" {
			return nil, fmt.Errorf("%s forge provider requires GITEA_URL", providerName)
		}

		provider := gateway.NewGiteaGateway(cfg.GiteaURL)
		if prefix := strings.TrimSpace(cfg.GiteaDraftTitlePrefix); prefix != "" {
			provider.WithDraftTitlePrefix(prefix)
		}

		publisher := basePublisher
		token := strings.TrimSpace(cfg.GiteaToken)
		username := strings.TrimSpace(cfg.GiteaUsername)
		if username != "" && token != "" {
			publisher = gateway.NewGiteaBranchPublisher(basePublisher, username, token)
		} else if token != "" && username == "" {
			logger.Warn("GITEA_USERNAME not configured; branch publication will use ambient Git credentials")
		}

		creator := prfactory.NewFactory(db, logger).
			WithForgeProvider(provider).
			WithForgeCredential(token).
			WithBranchPublisher(publisher).
			WithBranchRemote(remote)
		return &forgeIntegration{
			provider:   provider,
			credential: forge.Credential{Token: token},
			creator:    creator,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported forge provider %q", providerName)
	}
}
