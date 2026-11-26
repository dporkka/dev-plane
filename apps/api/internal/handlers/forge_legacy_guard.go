package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/models"
)

// requireGitHubRepository prevents legacy GitHub-only operations from
// interpreting a Gitea repository's owner/name as GitHub coordinates.
func requireGitHubRepository(w http.ResponseWriter, raw sql.NullString) bool {
	settings, err := models.ParseRepositoryForgeSettings([]byte(raw.String))
	if err != nil || settings.Provider != models.ForgeProviderGitHub {
		respond.Error(w, http.StatusConflict, errors.New("operation requires a repository with GitHub authority"))
		return false
	}
	return true
}
