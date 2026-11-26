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
	var lifecycle prfactory.GiteaBindingLifecycleRequest
	req := &lifecycle.GiteaBindingRequest
	var action string
	flag.StringVar(&action, "action", "bind", "bind, revoke, or reactivate")
	flag.StringVar(&lifecycle.InstanceURL, "instance-url", "", "exact canonical binding instance for lifecycle action")
	flag.Int64Var(&lifecycle.ExpectedGeneration, "expected-generation", 0, "current durable generation for lifecycle action")
	flag.StringVar(&lifecycle.Reason, "reason", "", "non-secret operator audit reason for lifecycle action")
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
	if action != "bind" && action != "revoke" && action != "reactivate" {
		return fmt.Errorf("unknown binding action")
	}
	if action == "bind" && (lifecycle.InstanceURL != "" || lifecycle.ExpectedGeneration != 0 || lifecycle.Reason != "") {
		return fmt.Errorf("lifecycle flags require revoke or reactivate")
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
	if action == "revoke" {
		generation, err := prfactory.RevokeGiteaRepositoryBinding(ctx, database, lifecycle)
		if err != nil {
			return err
		}
		fmt.Printf("Revoked Gitea repository binding: repo=%s generation=%d\n", req.RepositoryID, generation)
		return nil
	}
	forge, credential, err := prfactory.ConfiguredGiteaBindingForge()
	if err != nil {
		return err
	}
	var result prfactory.GiteaBinding
	if action == "reactivate" {
		result, err = prfactory.ReactivateGiteaRepositoryBinding(ctx, database, forge, credential, lifecycle)
	} else {
		result, err = prfactory.BindGiteaRepository(ctx, database, forge, credential, *req)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Verified Gitea repository binding: repo=%s project=%s organization=%s upstream_id=%d generation=%d\n", result.RepositoryID, result.ProjectID, result.OrganizationID, result.UpstreamRepositoryID, result.AuthorizationGeneration)
	return nil
}
