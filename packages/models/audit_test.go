package models

import (
	"database/sql"
	"testing"
	"time"
)

func TestAuditLog_Validate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		log := &AuditLog{
			OrganizationID: "org-1",
			ActorType:      ActorTypeUser,
			Action:         "task.created",
			ResourceType:   "task",
		}
		assertError(t, log.Validate(), false)
	})

	t.Run("missing organization_id", func(t *testing.T) {
		log := &AuditLog{
			ActorType:    ActorTypeUser,
			Action:       "task.created",
			ResourceType: "task",
		}
		err := log.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "audit log organization_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "audit log organization_id is required")
		}
	})

	t.Run("missing actor_type", func(t *testing.T) {
		log := &AuditLog{
			OrganizationID: "org-1",
			Action:         "task.created",
			ResourceType:   "task",
		}
		err := log.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "audit log actor_type is required" {
			t.Errorf("got %q, want %q", err.Error(), "audit log actor_type is required")
		}
	})

	t.Run("missing action", func(t *testing.T) {
		log := &AuditLog{
			OrganizationID: "org-1",
			ActorType:      ActorTypeUser,
			ResourceType:   "task",
		}
		err := log.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "audit log action is required" {
			t.Errorf("got %q, want %q", err.Error(), "audit log action is required")
		}
	})

	t.Run("missing resource_type", func(t *testing.T) {
		log := &AuditLog{
			OrganizationID: "org-1",
			ActorType:      ActorTypeUser,
			Action:         "task.created",
		}
		err := log.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "audit log resource_type is required" {
			t.Errorf("got %q, want %q", err.Error(), "audit log resource_type is required")
		}
	})
}

func TestActorType_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{ActorTypeUser, "user"},
		{ActorTypeAgent, "agent"},
		{ActorTypeSystem, "system"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestNullAuditLog(t *testing.T) {
	now := time.Now()
	log := NullAuditLog(
		sql.NullString{String: "id-1", Valid: true},
		sql.NullString{String: "org-1", Valid: true},
		sql.NullString{String: "user", Valid: true},
		sql.NullString{String: "actor-1", Valid: true},
		sql.NullString{String: "task.created", Valid: true},
		sql.NullString{String: "task", Valid: true},
		sql.NullString{String: "task-1", Valid: true},
		sql.NullString{String: `{"ip":"127.0.0.1"}`, Valid: true},
		sql.NullString{String: "127.0.0.1", Valid: true},
		sql.NullString{String: "Mozilla", Valid: true},
		sql.NullTime{Time: now, Valid: true},
	)

	if log.ID != "id-1" {
		t.Errorf("id = %q, want id-1", log.ID)
	}
	if log.OrganizationID != "org-1" {
		t.Errorf("organization_id = %q", log.OrganizationID)
	}
	if log.ActorID == nil || *log.ActorID != "actor-1" {
		t.Errorf("actor_id = %v", log.ActorID)
	}
	if log.ResourceID == nil || *log.ResourceID != "task-1" {
		t.Errorf("resource_id = %v", log.ResourceID)
	}
	if log.IPAddress == nil || *log.IPAddress != "127.0.0.1" {
		t.Errorf("ip_address = %v", log.IPAddress)
	}
	if log.UserAgent == nil || *log.UserAgent != "Mozilla" {
		t.Errorf("user_agent = %v", log.UserAgent)
	}
	if !log.CreatedAt.Equal(now) {
		t.Errorf("created_at = %v, want %v", log.CreatedAt, now)
	}
}

func TestNullAuditLog_Invalid(t *testing.T) {
	log := NullAuditLog(
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
		sql.NullTime{},
	)

	if log.ActorID != nil {
		t.Error("expected nil actor_id")
	}
	if log.ResourceID != nil {
		t.Error("expected nil resource_id")
	}
	if log.IPAddress != nil {
		t.Error("expected nil ip_address")
	}
}
