package main

import (
	"database/sql"
	"log/slog"
	"os"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/ai-dev-control-plane/worker/internal/handlers"
)

func buildWorkerForgeSelection() (*gateway.ForgeSelection, error) {
	return gateway.SelectForge(gateway.ForgeSettings{
		Provider:              envOrDefault("FORGE_PROVIDER", "github"),
		GitRemote:             envOrDefault("FORGE_GIT_REMOTE", "origin"),
		GitHubClientID:        os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret:    os.Getenv("GITHUB_CLIENT_SECRET"),
		GitHubToken:           os.Getenv("GITHUB_TOKEN"),
		GiteaURL:              os.Getenv("GITEA_URL"),
		GiteaToken:            os.Getenv("GITEA_TOKEN"),
		GiteaUsername:         os.Getenv("GITEA_USERNAME"),
		GiteaDraftTitlePrefix: envOrDefault("GITEA_DRAFT_TITLE_PREFIX", "WIP:"),
	})
}

func configureApprovalForge(handler *handlers.ApprovalHandler, db *sql.DB, logger *slog.Logger) error {
	selection, err := buildWorkerForgeSelection()
	if err != nil {
		return err
	}
	if selection == nil {
		return nil
	}

	creator := prfactory.NewFactory(db, logger).
		WithForgeProvider(selection.Provider).
		WithForgeCredential(selection.Credential.Token).
		WithBranchPublisher(selection.Publisher).
		WithBranchRemote(selection.Remote)
	handler.WithPullRequestCreator(creator)
	return nil
}
