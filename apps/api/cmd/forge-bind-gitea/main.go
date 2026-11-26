// Command forge-bind-gitea is an operator-only repository grant tool. It has
// no default database/tenant or automatic migration, and exposes no HTTP route.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/prfactory"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var req prfactory.GiteaBindingRequest
	var apply bool
	flag.StringVar(&req.RepositoryID, "repository-id", "", "explicit local repository ID")
	flag.StringVar(&req.ProjectID, "project-id", "", "explicit authorized project ID")
	flag.StringVar(&req.OrganizationID, "organization-id", "", "explicit authorized organization ID")
	flag.Int64Var(&req.UpstreamRepositoryID, "upstream-repository-id", 0, "independently verified Gitea repository ID")
	flag.BoolVar(&apply, "apply", false, "confirm the operator has authorized this exact tenant grant")
	flag.Parse()
	if !apply || flag.NArg() != 0 || req.RepositoryID == "" || req.ProjectID == "" || req.OrganizationID == "" || req.UpstreamRepositoryID <= 0 {
		return fmt.Errorf("explicit repository, project, organization, upstream ID and --apply are required")
	}
	forge, credential, err := prfactory.ConfiguredGiteaBindingForge()
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	var driver string
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		driver = "postgres"
	case strings.HasPrefix(dsn, "file:"):
		driver = "sqlite3"
	default:
		return fmt.Errorf("explicit postgres:// or file: DATABASE_URL required")
	}
	database, err := sql.Open(driver, dsn)
	if err != nil {
		return fmt.Errorf("cannot open configured database")
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if driver == "sqlite3" {
		if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
			return fmt.Errorf("cannot enable SQLite foreign keys")
		}
	}
	result, err := prfactory.BindGiteaRepository(ctx, database, forge, credential, req)
	if err != nil {
		return err
	}
	fmt.Printf("Verified Gitea repository binding: repo=%s project=%s organization=%s upstream_id=%d\n", result.RepositoryID, result.ProjectID, result.OrganizationID, result.UpstreamRepositoryID)
	return nil
}
