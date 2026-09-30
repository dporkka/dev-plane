package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/pkg/changeauthority"
	"github.com/ai-dev-control-plane/api/pkg/changesetpublisher"
	"github.com/ai-dev-control-plane/models"
)

type fakeChangeSetPublisher struct {
	changeSetID string
	actor       changeauthority.Actor
	progressSet bool
	result      *changesetpublisher.Result
	err         error
}

func (f *fakeChangeSetPublisher) Publish(ctx context.Context, changeSetID string, actor changeauthority.Actor, progress changesetpublisher.ProgressFunc) (*changesetpublisher.Result, error) {
	f.changeSetID = changeSetID
	f.actor = actor
	f.progressSet = progress != nil
	return f.result, f.err
}

func setupPublicationWorkerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			role TEXT NOT NULL,
			deleted_at DATETIME
		);
		INSERT INTO users (id, organization_id, role) VALUES ('user-1', 'org-1', 'owner');
	`); err != nil {
		db.Close()
		t.Fatalf("create fixture: %v", err)
	}
	return db
}

func TestHandleChangeSetPublishRequestedReloadsActorAndInvokesPublisher(t *testing.T) {
	db := setupPublicationWorkerDB(t)
	defer db.Close()

	publisher := &fakeChangeSetPublisher{result: &changesetpublisher.Result{
		ChangeSetID: "set-1", PublicationStatus: "completed",
	}}
	handler := NewChangeSetPublicationHandler(db, slog.Default(), nil).
		WithPublisher(publisher)

	err := handler.HandlePublishRequested(&nats.Msg{Data: []byte(`{
		"change_set_id":"set-1",
		"project_id":"project-1",
		"actor_id":"user-1",
		"organization_id":"org-1",
		"status":"requested"
	}`)})
	if err != nil {
		t.Fatalf("HandlePublishRequested() error: %v", err)
	}
	if publisher.changeSetID != "set-1" {
		t.Fatalf("change set id = %q, want set-1", publisher.changeSetID)
	}
	if publisher.actor.UserID != "user-1" ||
		publisher.actor.OrganizationID != "org-1" ||
		publisher.actor.Role != models.RoleOwner {
		t.Fatalf("actor = %#v", publisher.actor)
	}
	if !publisher.progressSet {
		t.Fatal("publisher progress heartbeat was not configured")
	}
}

func TestHandleChangeSetPublishRequestedAcksDeterministicBlock(t *testing.T) {
	db := setupPublicationWorkerDB(t)
	defer db.Close()

	publisher := &fakeChangeSetPublisher{err: &changesetpublisher.PublishError{
		Kind: changesetpublisher.ErrorBlocked,
		Err:  errors.New("github pull request is closed without merge"),
	}}
	handler := NewChangeSetPublicationHandler(db, slog.Default(), nil).
		WithPublisher(publisher)

	err := handler.HandlePublishRequested(&nats.Msg{Data: []byte(`{
		"change_set_id":"set-1",
		"actor_id":"user-1",
		"organization_id":"org-1"
	}`)})
	if err != nil {
		t.Fatalf("blocked publication should be handled and acked, got: %v", err)
	}
}

func TestHandleChangeSetPublishRequestedReturnsRetryableFailure(t *testing.T) {
	db := setupPublicationWorkerDB(t)
	defer db.Close()

	retryErr := errors.New("github unavailable")
	publisher := &fakeChangeSetPublisher{err: &changesetpublisher.PublishError{
		Kind: changesetpublisher.ErrorRetryable,
		Err:  retryErr,
	}}
	handler := NewChangeSetPublicationHandler(db, slog.Default(), nil).
		WithPublisher(publisher)

	err := handler.HandlePublishRequested(&nats.Msg{Data: []byte(`{
		"change_set_id":"set-1",
		"actor_id":"user-1",
		"organization_id":"org-1"
	}`)})
	if !errors.Is(err, retryErr) {
		t.Fatalf("HandlePublishRequested() error = %v, want retryable cause", err)
	}
}

func TestHandleChangeSetPublishRequestedDropsRevokedActor(t *testing.T) {
	db := setupPublicationWorkerDB(t)
	defer db.Close()
	if _, err := db.Exec("UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = 'user-1'"); err != nil {
		t.Fatalf("revoke actor: %v", err)
	}

	publisher := &fakeChangeSetPublisher{}
	handler := NewChangeSetPublicationHandler(db, slog.Default(), nil).
		WithPublisher(publisher)

	err := handler.HandlePublishRequested(&nats.Msg{Data: []byte(`{
		"change_set_id":"set-1",
		"actor_id":"user-1",
		"organization_id":"org-1"
	}`)})
	if err != nil {
		t.Fatalf("revoked actor event should be dropped, got: %v", err)
	}
	if publisher.changeSetID != "" {
		t.Fatalf("publisher invoked for revoked actor: %q", publisher.changeSetID)
	}
}
