package taskgraph

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			status TEXT NOT NULL,
			deleted_at TIMESTAMP
		);
		CREATE TABLE task_dependencies (
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			depends_on_task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (task_id, depends_on_task_id),
			CHECK (task_id <> depends_on_task_id)
		);
	`); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedTask(t *testing.T, db *sql.DB, id, repo, status string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO tasks (id, project_id, repository_id, status)
		VALUES (?, 'project-1', ?, ?)
	`, id, repo, status); err != nil {
		t.Fatal(err)
	}
}

func TestAddDependencyRejectsCyclesAndCrossRepositoryEdges(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	seedTask(t, db, "a", "repo-1", "approved")
	seedTask(t, db, "b", "repo-1", "approved")
	seedTask(t, db, "c", "repo-1", "approved")
	seedTask(t, db, "other", "repo-2", "approved")

	if err := AddDependency(ctx, db, "b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := AddDependency(ctx, db, "c", "b"); err != nil {
		t.Fatal(err)
	}
	if err := AddDependency(ctx, db, "a", "c"); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("cycle error = %v", err)
	}
	if err := AddDependency(ctx, db, "a", "a"); !errors.Is(err, ErrSelfDependency) {
		t.Fatalf("self dependency error = %v", err)
	}
	if err := AddDependency(ctx, db, "a", "other"); !errors.Is(err, ErrCrossRepository) {
		t.Fatalf("cross-repository error = %v", err)
	}
}

func TestListBlockersReflectsDependencyCompletion(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	seedTask(t, db, "root", "repo-1", "approved")
	seedTask(t, db, "done", "repo-1", "done")
	seedTask(t, db, "running", "repo-1", "running")
	if err := AddDependency(ctx, db, "root", "done"); err != nil {
		t.Fatal(err)
	}
	if err := AddDependency(ctx, db, "root", "running"); err != nil {
		t.Fatal(err)
	}

	blockers, err := ListBlockers(ctx, db, "root")
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 1 || blockers[0].DependencyTaskID != "running" {
		t.Fatalf("blockers = %+v", blockers)
	}

	if _, err := db.Exec("UPDATE tasks SET status = 'done' WHERE id = 'running'"); err != nil {
		t.Fatal(err)
	}
	blockers, err = ListBlockers(ctx, db, "root")
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("blockers = %+v", blockers)
	}
}

func TestEligibleDependentsRequiresEveryDependencyDone(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	seedTask(t, db, "a", "repo-1", "done")
	seedTask(t, db, "b", "repo-1", "running")
	seedTask(t, db, "child", "repo-1", "approved")
	seedTask(t, db, "not-approved", "repo-1", "backlog")
	for _, edge := range [][2]string{{"child", "a"}, {"child", "b"}, {"not-approved", "a"}} {
		if err := AddDependency(ctx, db, edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}

	eligible, err := EligibleDependents(ctx, db, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(eligible) != 0 {
		t.Fatalf("eligible = %v", eligible)
	}

	if _, err := db.Exec("UPDATE tasks SET status = 'done' WHERE id = 'b'"); err != nil {
		t.Fatal(err)
	}
	eligible, err = EligibleDependents(ctx, db, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(eligible) != 1 || eligible[0] != "child" {
		t.Fatalf("eligible = %v", eligible)
	}
}
