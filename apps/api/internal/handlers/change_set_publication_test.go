package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/events"
)

func TestGetChangeSetPublicationReturnsCheckpointProgress(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()
	decisionDigest, _ := decisionPacketFixture(t, "pr-1", "candidate-sha", "tree-a", now)
	setDigest, setManifest := authorizedChangeSetManifestFixture(t, decisionDigest)

	mock.ExpectQuery("SELECT id, project_id, name, description, status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "name", "description", "status", "publication_status",
			"publication_digest", "publication_manifest", "authorized_at", "authorized_by",
			"created_by", "created_at", "updated_at",
		}).AddRow(
			"set-1", "project-1", "Release 1", nil, "authorized", "blocked",
			setDigest, setManifest, now, testUserID, testUserID, now, now,
		))
	expectAuthorizeProject(mock, "project-1")
	mock.ExpectQuery("SELECT cp.candidate_id, cp.ordinal, cp.status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"candidate_id", "ordinal", "status", "attempt_count", "merge_sha", "last_error",
			"pull_request_id", "commit_sha", "task_id", "pr_state", "pr_number", "owner", "name",
		}).AddRow(
			"candidate-1", 0, "blocked", 2, nil, "merge conflict",
			"pr-1", "candidate-sha", "task-1", "open", 42, "owner", "repo",
		))

	rec := httptest.NewRecorder()
	h.GetChangeSetPublication(rec, newChangeSetRequest(
		http.MethodGet, "/change-sets/set-1/publication", "id", "set-1", "",
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"publication_status\":\"blocked\"") ||
		!strings.Contains(rec.Body.String(), "\"attempt_count\":2") ||
		!strings.Contains(rec.Body.String(), "merge conflict") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestPublishChangeSetEnqueuesWorkerRequest(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()
	decisionDigest, _ := decisionPacketFixture(t, "pr-1", "candidate-sha", "tree-a", now)
	setDigest, setManifest := authorizedChangeSetManifestFixture(t, decisionDigest)
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	mock.ExpectQuery("SELECT id, project_id, name, description, status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "name", "description", "status", "publication_status",
			"publication_digest", "publication_manifest", "authorized_at", "authorized_by",
			"created_by", "created_at", "updated_at",
		}).AddRow(
			"set-1", "project-1", "Release 1", nil, "authorized", "pending",
			setDigest, setManifest, now, testUserID, testUserID, now, now,
		))
	expectAuthorizeProject(mock, "project-1")

	rec := httptest.NewRecorder()
	h.PublishChangeSet(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/publish", "id", "set-1", "",
	))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if publisher.subject != events.ChangeSetPublishRequested {
		t.Fatalf("subject = %q, want %q", publisher.subject, events.ChangeSetPublishRequested)
	}
	var event events.ChangeSetPublicationEvent
	if err := json.Unmarshal(publisher.data, &event); err != nil {
		t.Fatalf("decode publication event: %v", err)
	}
	if event.ChangeSetID != "set-1" || event.ProjectID != "project-1" ||
		event.ActorID != testUserID || event.OrganizationID != testOrgID ||
		event.Status != "requested" {
		t.Fatalf("event = %#v", event)
	}
	if !strings.Contains(rec.Body.String(), "\"publication_status\":\"pending\"") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestPublishChangeSetRequiresEventBus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()
	decisionDigest, _ := decisionPacketFixture(t, "pr-1", "candidate-sha", "tree-a", now)
	setDigest, setManifest := authorizedChangeSetManifestFixture(t, decisionDigest)

	mock.ExpectQuery("SELECT id, project_id, name, description, status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "name", "description", "status", "publication_status",
			"publication_digest", "publication_manifest", "authorized_at", "authorized_by",
			"created_by", "created_at", "updated_at",
		}).AddRow(
			"set-1", "project-1", "Release 1", nil, "authorized", "pending",
			setDigest, setManifest, now, testUserID, testUserID, now, now,
		))
	expectAuthorizeProject(mock, "project-1")

	rec := httptest.NewRecorder()
	h.PublishChangeSet(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/publish", "id", "set-1", "",
	))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
}
