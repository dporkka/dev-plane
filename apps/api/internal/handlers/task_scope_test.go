package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

func TestInsertTaskEnforcesRepositoryProjectAndCreatorTenant(t *testing.T) {
	driver, dsn := "sqlite3", ":memory:"
	if external := os.Getenv("DEVPLANE_TEST_TASK_SCOPE_POSTGRES_URL"); external != "" {
		parsed, err := url.Parse(external)
		if err != nil || parsed.Scheme != "postgres" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" ||
			parsed.User == nil || parsed.User.String() != "fixture" || parsed.Path != "/devplane_task_scope_test" ||
			parsed.RawQuery != "sslmode=disable" || parsed.Fragment != "" {
			t.Fatal("expected disposable loopback task fixture")
		}
		driver, dsn = "postgres", external
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if driver == "postgres" {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pg_tables WHERE schemaname='public'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("fixture must be empty: %d %v", count, err)
		}
	}
	if err := goose.SetDialect(driver); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
        INSERT INTO organizations(id,name,slug) VALUES ('00000001-0000-4000-8000-000000000001','One','one'),('00000001-0000-4000-8000-000000000002','Two','two');
        INSERT INTO projects(id,organization_id,name,slug) VALUES ('00000002-0000-4000-8000-000000000001','00000001-0000-4000-8000-000000000001','One','one'),('00000002-0000-4000-8000-000000000002','00000001-0000-4000-8000-000000000002','Two','two');
        INSERT INTO users(id,organization_id,email) VALUES ('00000004-0000-4000-8000-000000000001','00000001-0000-4000-8000-000000000001','one@example.invalid'),('00000004-0000-4000-8000-000000000002','00000001-0000-4000-8000-000000000002','two@example.invalid');
        INSERT INTO repositories(id,project_id,owner,name,full_name,clone_url)
        VALUES ('00000003-0000-4000-8000-000000000001','00000002-0000-4000-8000-000000000001','acme','widget','acme/widget','https://github.com/acme/widget.git');
    `); err != nil {
		t.Fatal(err)
	}
	h := &Handler{db: db}
	opts := createTaskOptions{ProjectID: "00000002-0000-4000-8000-000000000001", RepositoryID: "00000003-0000-4000-8000-000000000001", CreatedBy: "00000004-0000-4000-8000-000000000001", Title: "fixture"}
	if _, err := h.insertTask(context.Background(), opts); err != nil {
		t.Fatalf("same tenant and project rejected: %v", err)
	}
	opts.ProjectID = "00000002-0000-4000-8000-000000000002"
	opts.CreatedBy = "00000004-0000-4000-8000-000000000002"
	if _, err := h.insertTask(context.Background(), opts); !errors.Is(err, errTaskRepositoryScope) {
		t.Fatalf("foreign repository accepted: %v", err)
	}
	opts.ProjectID = "00000002-0000-4000-8000-000000000001"
	if _, err := h.insertTask(context.Background(), opts); !errors.Is(err, errTaskRepositoryScope) {
		t.Fatalf("foreign creator accepted: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count); err != nil || count != 1 {
		t.Fatalf("task count=%d err=%v", count, err)
	}
}
