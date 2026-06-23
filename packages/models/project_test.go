package models

import (
	"database/sql"
	"testing"
	"time"
)

func TestProject_Validate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		p := &Project{
			Name:           "My Project",
			Slug:           "my-project",
			OrganizationID: "org-1",
		}
		assertError(t, p.Validate(), false)
	})

	t.Run("missing name", func(t *testing.T) {
		p := &Project{
			Slug:           "my-project",
			OrganizationID: "org-1",
		}
		err := p.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "project name is required" {
			t.Errorf("got %q, want %q", err.Error(), "project name is required")
		}
	})

	t.Run("missing slug", func(t *testing.T) {
		p := &Project{
			Name:           "My Project",
			OrganizationID: "org-1",
		}
		err := p.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "project slug is required" {
			t.Errorf("got %q, want %q", err.Error(), "project slug is required")
		}
	})

	t.Run("missing organization_id", func(t *testing.T) {
		p := &Project{
			Name: "My Project",
			Slug: "my-project",
		}
		err := p.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "project organization_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "project organization_id is required")
		}
	})
}

func TestNullProject(t *testing.T) {
	now := time.Now()
	p := NullProject(
		sql.NullString{String: "id-1", Valid: true},
		sql.NullString{String: "org-1", Valid: true},
		sql.NullString{String: "My Project", Valid: true},
		sql.NullString{String: "my-project", Valid: true},
		sql.NullString{String: "A project", Valid: true},
		sql.NullString{String: `{"theme":"dark"}`, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{},
	)

	if p.ID != "id-1" {
		t.Errorf("id = %q", p.ID)
	}
	if p.Name != "My Project" {
		t.Errorf("name = %q", p.Name)
	}
	if p.Description == nil || *p.Description != "A project" {
		t.Errorf("description = %v", p.Description)
	}
	if string(p.Settings) != `{"theme":"dark"}` {
		t.Errorf("settings = %q", p.Settings)
	}
	if p.DeletedAt != nil {
		t.Error("expected nil deleted_at")
	}
}
