package handlers

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/ai-dev-control-plane/prfactory"
	"github.com/nats-io/nats.go"
)

func TestHandleTaskApprovedBlocksUnboundGiteaAndQueuedReplay(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "initial"
		if queued {
			name = "queued-replay"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("GITEA_URL", "http://127.0.0.1:1")
			t.Setenv("GITEA_BINDING_GENERATION", "1")
			t.Setenv("GITEA_TOKEN", "fixture")
			db := setupTaskHandlerDB(t)
			defer db.Close()
			insertApprovedTaskFixture(t, db)
			if _, err := db.Exec(`
                CREATE TABLE gitea_repository_bindings(repository_id TEXT,project_id TEXT,organization_id TEXT,instance_url TEXT,upstream_repository_id INTEGER,full_name TEXT,authorization_generation INTEGER DEFAULT 1,revoked_at TEXT);
                UPDATE repositories SET owner='acme',name='widget',full_name='acme/widget',clone_url='http://127.0.0.1:1/acme/widget.git',
                settings='{"forge":{"provider":"gitea","base_url":"http://127.0.0.1:1"}}';
            `); err != nil {
				t.Fatal(err)
			}
			if queued {
				if _, err := db.Exec(`INSERT INTO agent_runs(id,task_id,agent_role,status) VALUES ('run-1','task-1','implementer','queued')`); err != nil {
					t.Fatal(err)
				}
			}
			provider := &fakeRuntimeProvider{}
			publisher := &fakeWorkerEventPublisher{}
			handler := NewTaskHandler(db, slog.Default()).WithRuntimeProvider(provider, "local").WithEventPublisher(publisher)
			err := handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved","data":{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}}`)})
			if !errors.Is(err, prfactory.ErrGiteaRepositoryBinding) {
				t.Fatalf("expected binding rejection: %v", err)
			}
			if provider.req.CloneURL != "" || publisher.subject != "" {
				t.Fatal("unbound repository created workspace or published run")
			}
			var status string
			if err := db.QueryRow("SELECT status FROM tasks WHERE id='task-1'").Scan(&status); err != nil || status != "approved" {
				t.Fatalf("task status=%q err=%v", status, err)
			}
		})
	}
}
