package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"fmt"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// Repository represents a repository record.
type Repository struct {
	ID               string          `json:"id"`
	ProjectID        string          `json:"project_id"`
	GitHubID          *int64          `json:"github_id,omitempty"`
	ForgeProvider     string          `json:"forge_provider"`
	ForgeBaseURL      string          `json:"forge_base_url"`
	ForgeRepositoryID *string         `json:"forge_repository_id,omitempty"`
	VCSBackend        string          `json:"vcs_backend"`
	Owner            string          `json:"owner"`
	Name             string          `json:"name"`
	FullName         string          `json:"full_name"`
	CloneURL         string          `json:"clone_url"`
	DefaultBranch    string          `json:"default_branch"`
	Private          bool            `json:"private"`
	ConnectionStatus string          `json:"connection_status"`
	LastSyncedAt     *time.Time      `json:"last_synced_at,omitempty"`
	Settings         json.RawMessage `json:"settings,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ConnectRepositoryRequest is the request body for connecting a repository.
type ConnectRepositoryRequest struct {
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Provider      string `json:"provider,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`
	VCSBackend    string `json:"vcs_backend,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// ListRepositories returns all repositories for a project.
func (h *Handler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	if err := authz.AuthorizeProject(ctx, h.db, user, projectID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("project not found"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, project_id, github_id, forge_provider, forge_base_url, forge_repository_id, vcs_backend,
		       owner, name, full_name, clone_url, default_branch, private, connection_status,
		       last_synced_at, settings, created_at, updated_at
		FROM repositories
		WHERE project_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var repos []Repository
	for rows.Next() {
		var repo Repository
		var ghID sql.NullInt64
		var forgeRepositoryID, settings sql.NullString
		var lastSync sql.NullTime
		if err := rows.Scan(&repo.ID, &repo.ProjectID, &ghID, &repo.ForgeProvider, &repo.ForgeBaseURL,
			&forgeRepositoryID, &repo.VCSBackend, &repo.Owner, &repo.Name, &repo.FullName, &repo.CloneURL,
			&repo.DefaultBranch, &repo.Private, &repo.ConnectionStatus, &lastSync, &settings,
			&repo.CreatedAt, &repo.UpdatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if ghID.Valid {
			repo.GitHubID = &ghID.Int64
		}
		if forgeRepositoryID.Valid {
			repo.ForgeRepositoryID = &forgeRepositoryID.String
		}
		if lastSync.Valid {
			repo.LastSyncedAt = &lastSync.Time
		}
		if settings.Valid {
			repo.Settings = json.RawMessage(settings.String)
		}
		repos = append(repos, repo)
	}

	if repos == nil {
		repos = []Repository{}
	}
	respond.JSON(w, http.StatusOK, repos)
}

// ConnectRepository connects a GitHub repository to a project.
func (h *Handler) ConnectRepository(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	if err := authz.AuthorizeProject(ctx, h.db, user, projectID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("project not found"))
		return
	}

	var req ConnectRepositoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	req.Owner = strings.TrimSpace(req.Owner)
	req.Name = strings.TrimSpace(req.Name)
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.VCSBackend = strings.ToLower(strings.TrimSpace(req.VCSBackend))
	if req.Provider == "" {
		req.Provider = "github"
	}
	if req.VCSBackend == "" {
		req.VCSBackend = "git"
	}
	if req.VCSBackend == "jujutsu" {
		req.VCSBackend = "jj"
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.Owner == "" || req.Name == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("owner and name are required"))
		return
	}
	if req.Provider != "github" && req.Provider != "gitea" {
		respond.Error(w, http.StatusBadRequest, errors.New("provider must be github or gitea"))
		return
	}
	if req.VCSBackend != "git" && req.VCSBackend != "jj" {
		respond.Error(w, http.StatusBadRequest, errors.New("vcs_backend must be git or jj"))
		return
	}
	if req.Provider == "github" {
		if err := validateGitHubOwner(req.Owner); err != nil {
			respond.Error(w, http.StatusBadRequest, err)
			return
		}
	} else if err := validateForgeNamespace(req.Owner); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if err := validateGitHubRepoName(req.Name); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		if req.Provider == "github" {
			baseURL = "https://github.com"
		} else {
			baseURL = "https://gitea.com"
		}
	}
	baseURL, err := normalizeForgeBaseURL(baseURL)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	id := uuid.New().String()
	now := time.Now().UTC()
	fullName := req.Owner + "/" + req.Name
	cloneURL := baseURL + "/" + fullName + ".git"

	_, err = h.db.ExecContext(ctx, `
		INSERT INTO repositories (id, project_id, forge_provider, forge_base_url, vcs_backend,
			owner, name, full_name, clone_url, default_branch, connection_status, settings, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'connected', '{}', $11, $12)
	`, id, projectID, req.Provider, baseURL, req.VCSBackend, req.Owner, req.Name, fullName, cloneURL,
		req.DefaultBranch, now, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusCreated, Repository{
		ID:               id,
		ProjectID:        projectID,
		ForgeProvider:    req.Provider,
		ForgeBaseURL:     baseURL,
		VCSBackend:       req.VCSBackend,
		Owner:            req.Owner,
		Name:             req.Name,
		FullName:         fullName,
		CloneURL:         cloneURL,
		DefaultBranch:    req.DefaultBranch,
		ConnectionStatus: "connected",
		CreatedAt:        now,
		UpdatedAt:        now,
	})
}

func normalizeForgeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil {
		return "", fmt.Errorf("invalid forge base_url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("forge base_url must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("forge base_url must be an absolute URL without credentials, query, or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateForgeNamespace(owner string) error {
	if owner == "" || len(owner) > 255 {
		return errors.New("owner must be between 1 and 255 characters")
	}
	if strings.Contains(owner, "/") || strings.Contains(owner, "\\") || owner == "." || owner == ".." {
		return errors.New("owner is invalid")
	}
	for _, r := range owner {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return errors.New("owner may only contain letters, numbers, dots, hyphens, and underscores")
	}
	return nil
}

func validateGitHubOwner(owner string) error {
	if len(owner) > 39 {
		return errors.New("owner must be 39 characters or fewer")
	}
	if strings.HasPrefix(owner, "-") || strings.HasSuffix(owner, "-") {
		return errors.New("owner cannot start or end with a hyphen")
	}
	for _, r := range owner {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return errors.New("owner may only contain letters, numbers, and hyphens")
	}
	return nil
}

func validateGitHubRepoName(name string) error {
	if len(name) > 100 {
		return errors.New("repository name must be 100 characters or fewer")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return errors.New("repository name cannot contain path separators")
	}
	if name == "." || name == ".." {
		return errors.New("repository name is invalid")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return errors.New("repository name may only contain letters, numbers, dots, hyphens, and underscores")
	}
	return nil
}

// GetRepository returns a single repository by ID.
func (h *Handler) GetRepository(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository id is required"))
		return
	}

	if err := authz.AuthorizeRepository(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
		return
	}

	var repo Repository
	var ghID sql.NullInt64
	var forgeRepositoryID, settings sql.NullString
	var lastSync sql.NullTime
	err := h.db.QueryRowContext(ctx, `
		SELECT id, project_id, github_id, forge_provider, forge_base_url, forge_repository_id, vcs_backend,
		       owner, name, full_name, clone_url, default_branch, private, connection_status,
		       last_synced_at, settings, created_at, updated_at
		FROM repositories
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&repo.ID, &repo.ProjectID, &ghID, &repo.ForgeProvider, &repo.ForgeBaseURL,
		&forgeRepositoryID, &repo.VCSBackend, &repo.Owner, &repo.Name, &repo.FullName, &repo.CloneURL,
		&repo.DefaultBranch, &repo.Private, &repo.ConnectionStatus, &lastSync, &settings,
		&repo.CreatedAt, &repo.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if ghID.Valid {
		repo.GitHubID = &ghID.Int64
	}
	if forgeRepositoryID.Valid {
		repo.ForgeRepositoryID = &forgeRepositoryID.String
	}
	if lastSync.Valid {
		repo.LastSyncedAt = &lastSync.Time
	}
	if settings.Valid {
		repo.Settings = json.RawMessage(settings.String)
	}

	respond.JSON(w, http.StatusOK, repo)
}

// DisconnectRepository soft-deletes a repository connection.
func (h *Handler) DisconnectRepository(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository id is required"))
		return
	}

	if err := authz.AuthorizeRepository(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
		return
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE repositories SET deleted_at = $1, connection_status = 'disconnected', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// SyncRepository triggers a sync for a repository.
func (h *Handler) SyncRepository(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository id is required"))
		return
	}

	if err := authz.AuthorizeRepository(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
		return
	}

	now := time.Now().UTC()
	_, err := h.db.ExecContext(ctx, `
		UPDATE repositories SET last_synced_at = $1, connection_status = 'connected', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "syncing"})
}
