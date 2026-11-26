// Command forge-check-gitea-merge performs read-only revision preflight.
// Success is not merge approval and never executes a merge.
package main

import (
	"context"
	"database/sql"
	"errors"
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
	var req prfactory.GiteaMergePreflightRequest
	flag.StringVar(&req.PullRequestID, "pr-id", "", "local pull request ID")
	flag.StringVar(&req.RepositoryID, "repository-id", "", "local repository ID")
	flag.StringVar(&req.ProjectID, "project-id", "", "project ID")
	flag.StringVar(&req.OrganizationID, "organization-id", "", "organization ID")
	flag.StringVar(&req.ExpectedHeadSHA, "head", "", "full independently reviewed head SHA")
	flag.StringVar(&req.ExpectedBaseSHA, "base", "", "full independently reviewed base SHA")
	flag.Int64Var(&req.ExpectedGeneration, "expected-generation", 0, "current binding generation")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	dsn := os.Getenv("DATABASE_URL")
	var driver string
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		driver = "postgres"
	case strings.HasPrefix(dsn, "file:"):
		driver = "sqlite3"
	default:
		return errors.New("explicit postgres:// or file: DATABASE_URL required")
	}
	database, err := sql.Open(driver, dsn)
	if err != nil {
		return errors.New("cannot open configured database")
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := prfactory.CheckGiteaMergePreflight(ctx, database, req)
	if err != nil {
		return err
	}
	fmt.Printf("Revision preflight passed: repository=%d PR=%d head=%s base=%s\n", result.UpstreamRepositoryID, result.Number, result.HeadSHA, result.BaseSHA)
	fmt.Println("Not merge approval: CI evidence, policy, branch protection and fresh authorization remain required.")
	return nil
}
