package models

import (
	"database/sql"
	"testing"
	"time"
)

func TestConnectionStatus_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{ConnectionStatusPending, "pending"},
		{ConnectionStatusConnected, "connected"},
		{ConnectionStatusError, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestRepository_Validate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		r := &Repository{
			Owner:     "acme",
			Name:      "app",
			FullName:  "acme/app",
			CloneURL:  "https://github.com/acme/app.git",
			ProjectID: "proj-1",
		}
		assertError(t, r.Validate(), false)
	})

	t.Run("missing owner", func(t *testing.T) {
		r := &Repository{
			Name:      "app",
			FullName:  "acme/app",
			CloneURL:  "https://github.com/acme/app.git",
			ProjectID: "proj-1",
		}
		err := r.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "repository owner is required" {
			t.Errorf("got %q, want %q", err.Error(), "repository owner is required")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		r := &Repository{
			Owner:     "acme",
			FullName:  "acme/app",
			CloneURL:  "https://github.com/acme/app.git",
			ProjectID: "proj-1",
		}
		err := r.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "repository name is required" {
			t.Errorf("got %q, want %q", err.Error(), "repository name is required")
		}
	})

	t.Run("missing full_name", func(t *testing.T) {
		r := &Repository{
			Owner:     "acme",
			Name:      "app",
			CloneURL:  "https://github.com/acme/app.git",
			ProjectID: "proj-1",
		}
		err := r.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "repository full_name is required" {
			t.Errorf("got %q, want %q", err.Error(), "repository full_name is required")
		}
	})

	t.Run("missing clone_url", func(t *testing.T) {
		r := &Repository{
			Owner:     "acme",
			Name:      "app",
			FullName:  "acme/app",
			ProjectID: "proj-1",
		}
		err := r.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "repository clone_url is required" {
			t.Errorf("got %q, want %q", err.Error(), "repository clone_url is required")
		}
	})

	t.Run("missing project_id", func(t *testing.T) {
		r := &Repository{
			Owner:    "acme",
			Name:     "app",
			FullName: "acme/app",
			CloneURL: "https://github.com/acme/app.git",
		}
		err := r.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "repository project_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "repository project_id is required")
		}
	})
}

func TestNullRepository(t *testing.T) {
	now := time.Now()
	var githubID int64 = 42
	r := NullRepository(
		sql.NullString{String: "id-1", Valid: true},
		sql.NullString{String: "proj-1", Valid: true},
		sql.NullInt64{Int64: githubID, Valid: true},
		sql.NullString{String: "acme", Valid: true},
		sql.NullString{String: "app", Valid: true},
		sql.NullString{String: "acme/app", Valid: true},
		sql.NullString{String: "https://github.com/acme/app.git", Valid: true},
		sql.NullString{String: "main", Valid: true},
		sql.NullBool{Bool: true, Valid: true},
		sql.NullString{String: ConnectionStatusConnected, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullString{String: "secret", Valid: true},
		sql.NullString{String: `{"key":"value"}`, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{},
	)

	if r.ID != "id-1" {
		t.Errorf("id = %q", r.ID)
	}
	if r.GitHubID == nil || *r.GitHubID != 42 {
		t.Errorf("github_id = %v", r.GitHubID)
	}
	if !r.Private {
		t.Error("expected private true")
	}
	if r.ConnectionStatus != ConnectionStatusConnected {
		t.Errorf("connection_status = %q", r.ConnectionStatus)
	}
	if r.WebhookSecret == nil || *r.WebhookSecret != "secret" {
		t.Errorf("webhook_secret = %v", r.WebhookSecret)
	}
	if r.DeletedAt != nil {
		t.Error("expected nil deleted_at")
	}
}
