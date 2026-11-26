package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/runtimes"
	"github.com/nats-io/nats.go"
)

func TestHandleTaskApprovedRejectsBoundGiteaOnNonDockerRuntime(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "name": "widget", "full_name": "acme/widget", "html_url": root + "/acme/widget", "clone_url": root + "/acme/widget.git", "default_branch": "main"})
	}))
	defer srv.Close()
	t.Setenv("GITEA_URL", srv.URL)
	t.Setenv("GITEA_TOKEN", "synthetic-api")
	if _, err := db.Exec(`CREATE TABLE gitea_repository_bindings(repository_id TEXT,project_id TEXT,organization_id TEXT,instance_url TEXT,upstream_repository_id INTEGER,full_name TEXT);`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE repositories SET owner='acme',name='widget',full_name='acme/widget',clone_url=$1,settings=$2`,
		srv.URL+"/acme/widget.git", fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO gitea_repository_bindings VALUES ('repo-1','project-1','org-1',$1,77,'acme/widget')", srv.URL); err != nil {
		t.Fatal(err)
	}
	provider := &fakeRuntimeProvider{}
	publisher := &fakeWorkerEventPublisher{}
	h := NewTaskHandler(db, slog.Default()).WithRuntimeProvider(provider, "docker").WithEventPublisher(publisher)
	// A runtime name alone cannot substitute for a verified Docker provider.
	err := h.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved"}`)})
	if !errors.Is(err, runtimes.ErrGiteaWorkspaceAuthority) || provider.req.CloneURL != "" || publisher.subject != "" {
		t.Fatalf("non-Docker side effect: %v", err)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id='task-1'").Scan(&status); err != nil || status != "approved" {
		t.Fatalf("task changed: %s %v", status, err)
	}
}
