package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/db"
)

// Unlike the temporary-table dialect check, this test uses the actual Goose
// migration chain, UUID/FK/JSONB schema, and candidate admission queries.
// Only point POSTGRES_MIGRATED_TEST_DATABASE_URL at an empty disposable CI DB.
func TestSchedulerAdmissionMigratedPostgresTenantScope(t *testing.T) {
	if os.Getenv("RUN_MIGRATED_POSTGRES_TEST") != "1" {
		t.Skip("set RUN_MIGRATED_POSTGRES_TEST=1 and a disposable PostgreSQL test database")
	}
	dsn := os.Getenv("POSTGRES_MIGRATED_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("POSTGRES_MIGRATED_TEST_DATABASE_URL must point at an empty disposable test database")
	}
	database, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatalf("real Postgres migrations failed: %v", err)
	}

	orgA, orgB := uuid.NewString(), uuid.NewString()
	projectA, projectB := uuid.NewString(), uuid.NewString()
	repoA, repoA2, repoB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	userA, userB := uuid.NewString(), uuid.NewString()
	for _, item := range []struct{ id, name, slug string }{{orgA, "Admit A", "admit-a-" + orgA}, {orgB, "Admit B", "admit-b-" + orgB}} {
		if _, err := database.Exec(`INSERT INTO organizations(id,name,slug) VALUES ($1,$2,$3)`, item.id, item.name, item.slug); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, org, email string }{{userA, orgA, "a@example.invalid"}, {userB, orgB, "b@example.invalid"}} {
		if _, err := database.Exec(`INSERT INTO users(id,organization_id,email) VALUES ($1,$2,$3)`, item.id, item.org, item.email); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, org, name, slug string }{{projectA, orgA, "Project A", "project-a"}, {projectB, orgB, "Project B", "project-b"}} {
		if _, err := database.Exec(`INSERT INTO projects(id,organization_id,name,slug) VALUES ($1,$2,$3,$4)`, item.id, item.org, item.name, item.slug); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, project, name string }{{repoA, projectA, "repo-a"}, {repoA2, projectA, "repo-a2"}, {repoB, projectB, "repo-b"}} {
		if _, err := database.Exec(`INSERT INTO repositories(id,project_id,owner,name,full_name,clone_url) VALUES ($1,$2,'test',$3,$4,$5)`,
			item.id, item.project, item.name, "test/"+item.name, "https://example.invalid/"+item.name); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name, depProject, depRepo, depUser, depStatus string
		deleted, exists, allow                        bool
	}{
		{"same repository done", projectA, repoA, userA, "done", false, true, true},
		{"same repository pending", projectA, repoA, userA, "running", false, true, false},
		{"other repository done", projectA, repoA2, userA, "done", false, true, false},
		{"other organization done", projectB, repoB, userB, "done", false, true, false},
		{"soft deleted done", projectA, repoA, userA, "done", true, true, false},
		{"missing prerequisite", "", "", "", "", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dep, task, run := uuid.NewString(), uuid.NewString(), uuid.NewString()
			if tc.exists {
				_, err = database.Exec(`INSERT INTO tasks(id,project_id,repository_id,created_by,title,status,metadata) VALUES ($1,$2,$3,$4,$5,$6,'{}'::jsonb)`,
					dep, tc.depProject, tc.depRepo, tc.depUser, "Test prerequisite", tc.depStatus)
				if err != nil {
					t.Fatal(err)
				}
				if tc.deleted {
					if _, err = database.Exec(`UPDATE tasks SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, dep); err != nil {
						t.Fatal(err)
					}
				}
			}
			metadata := fmt.Sprintf(`{"scheduler":{"owns":["src/test-%s"],"depends_on":["%s"]}}`, task, dep)
			_, err = database.Exec(`INSERT INTO tasks(id,project_id,repository_id,created_by,title,status,metadata) VALUES ($1,$2,$3,$4,'Candidate','approved',$5::jsonb)`,
				task, projectA, repoA, userA, metadata)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = database.Exec(`INSERT INTO agent_runs(id,task_id,status) VALUES ($1,$2,'queued')`, run, task); err != nil {
				t.Fatal(err)
			}
			admission := NewSchedulerAdmission(database.DB, SchedulerCapacity{MaxParallel: 3, CPU: 8, MemoryMB: 4096})
			decision, err := admission.AdmitRun(context.Background(), run, task)
			if err != nil {
				t.Fatalf("AdmitRun: %v", err)
			}
			if decision.Allowed != tc.allow {
				t.Fatalf("allowed=%v, reason=%q, want %v", decision.Allowed, decision.Reason, tc.allow)
			}
			if !tc.allow {
				if tc.depStatus != "running" && decision.Reason != "dependency-unavailable" {
					t.Fatalf("out-of-scope prerequisite reason leaked or incorrect: %q", decision.Reason)
				}
				var status string
				if err := database.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, run).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "queued" {
					t.Fatalf("denied run status=%q, want queued", status)
				}
			}
		})
	}
}
