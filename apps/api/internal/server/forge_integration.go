package server

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/ai-dev-control-plane/api/internal/config"
	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/prfactory"
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

	selection, err := gateway.SelectForge(gateway.ForgeSettings{
		Provider:              cfg.ForgeProvider,
		GitRemote:             cfg.ForgeGitRemote,
		GitHubClientID:        cfg.GitHubClientID,
		GitHubClientSecret:    cfg.GitHubSecret,
		GitHubToken:           cfg.GitHubToken,
		GiteaURL:              cfg.GiteaURL,
		GiteaToken:            cfg.GiteaToken,
		GiteaUsername:         cfg.GiteaUsername,
		GiteaDraftTitlePrefix: cfg.GiteaDraftTitlePrefix,
	})
	if err != nil {
		return nil, err
	}
	if selection == nil {
		return nil, nil
	}

	creator := prfactory.NewFactory(db, logger).
		WithForgeProvider(selection.Provider).
		WithForgeCredential(selection.Credential.Token).
		WithBranchPublisher(selection.Publisher).
		WithBranchRemote(selection.Remote)

	return &forgeIntegration{
		provider:   selection.Provider,
		credential: selection.Credential,
		creator:    creator,
	}, nil
}
