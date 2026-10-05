package prfactory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/models"
)

type repositoryTarget struct {
	Owner string
	Name  string
	Forge models.RepositoryForgeSettings
}

// getRepositoryTarget loads the repository-owned forge authority. Existing rows
// without a forge settings block resolve to GitHub for backward compatibility.
func (f *Factory) getRepositoryTarget(ctx context.Context, repositoryID string) (repositoryTarget, error) {
	if f.db == nil {
		return repositoryTarget{}, fmt.Errorf("database is not configured")
	}
	var owner, name string
	var settings sql.NullString
	if err := f.db.QueryRowContext(ctx, `
		SELECT owner, name, settings FROM repositories
		WHERE id = $1 AND deleted_at IS NULL
	`, repositoryID).Scan(&owner, &name, &settings); err != nil {
		return repositoryTarget{}, fmt.Errorf("get repository target: %w", err)
	}

	var raw []byte
	if settings.Valid {
		raw = []byte(settings.String)
	}
	forge, err := models.ParseRepositoryForgeSettings(raw)
	if err != nil {
		return repositoryTarget{}, fmt.Errorf("parse repository forge settings: %w", err)
	}
	return repositoryTarget{Owner: owner, Name: name, Forge: forge}, nil
}

// validateRepositoryForge fails closed when process-level forge configuration
// does not match the repository's persisted forge authority. The subsequent
// wiring slice calls this before any Git push or PR publication side effect.
func (f *Factory) validateRepositoryForge(target repositoryTarget) error {
	if f.forge == nil {
		return fmt.Errorf("forge is not configured")
	}
	configured := strings.ToLower(strings.TrimSpace(f.forge.Name()))
	required := strings.ToLower(strings.TrimSpace(string(target.Forge.Provider)))
	if configured != required {
		return fmt.Errorf("repository requires forge provider %s, configured forge is %s", required, configured)
	}
	return nil
}
