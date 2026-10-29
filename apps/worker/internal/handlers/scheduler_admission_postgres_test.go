package handlers

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// This suite exercises the actual Postgres driver/dialect rather than the
// SQLite fixture. It intentionally uses ephemeral TEMP tables: it is a
// scheduler-policy smoke test, not a substitute for staging/RLS validation.
func TestSchedulerAdmissionPostgresTenantScope(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DATABASE_URL to test PostgreSQL scheduling isolation")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// TEMP tables live in a single session and are discarded on close.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TEMP TABLE tasks (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  repository_id TEXT NOT NULL,
  status TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  deleted_at TIMESTAMPTZ
);
CREATE TEMP TABLE agent_runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  status TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TEMP TABLE task_specs (
  task_id TEXT PRIMARY KEY,
  files_to_change TEXT NOT NULL DEFAULT '[]',
  files_to_create TEXT NOT NULL DEFAULT '[]'
);
`)
	if err != nil {
		t.Fatalf("create Postgres test tables: %v", err)
	}
	cases := []struct {
		name, project, repository, prerequisiteStatus string
		deleted, exists, allow                        bool
	}{
		{name: "local completed prerequisite", project: "project-a", repository: "repo-a", prerequisiteStatus: "done", exists: true, allow: true},
		{name: "local pending prerequisite", project: "project-a", repository: "repo-a", prerequisiteStatus: "running", exists: true},
		{name: "foreign project completed prerequisite", project: "project-b", repository: "repo-a", prerequisiteStatus: "done", exists: true},
		{name: "foreign repository completed prerequisite", project: "project-a", repository: "repo-b", prerequisiteStatus: "done", exists: true},
		{name: "deleted prerequisite", project: "project-a", repository: "repo-a", prerequisiteStatus: "done", exists: true, deleted: true},
		{name: "missing prerequisite", exists: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec("DELETE FROM agent_runs; DELETE FROM tasks"); err != nil {
				t.Fatal(err)
			}
			if tc.exists {
				_, err := db.Exec(`INSERT INTO tasks (id,project_id,repository_id,status,metadata,deleted_at)
VALUES ($1,$2,$3,$4,$5,CASE WHEN $6 THEN CURRENT_TIMESTAMP ELSE NULL END)`,
					"dependency", tc.project, tc.repository, tc.prerequisiteStatus, `{}`, tc.deleted)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := db.Exec(`INSERT INTO tasks (id,project_id,repository_id,status,metadata)
VALUES ($1,$2,$3,$4,$5)`, "candidate", "project-a", "repo-a", "approved",
				`{"scheduler":{"owns":["src/candidate"],"depends_on":["dependency"]}}`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`INSERT INTO agent_runs (id,task_id,status,metadata)
VALUES ($1,$2,$3,$4)`, "run-candidate", "candidate", "queued", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 2048})
			decision, err := admission.AdmitRun(context.Background(), "run-candidate", "candidate")
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != tc.allow {
				t.Fatalf("allowed=%v, reason=%q, want=%v", decision.Allowed, decision.Reason, tc.allow)
			}
			if !tc.allow {
				unavailable := !tc.exists || tc.deleted || tc.project != "project-a" || tc.repository != "repo-a"
				if unavailable && decision.Reason != "dependency-unavailable" {
					t.Fatalf("out-of-scope prerequisite was not masked: %q", decision.Reason)
				}
				var status string
				if err := db.QueryRow("SELECT status FROM agent_runs WHERE id=$1", "run-candidate").Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "queued" {
					t.Fatalf("denied run changed status to %q", status)
				}
			}
		})
	}
}
