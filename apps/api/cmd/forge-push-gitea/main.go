// Command forge-push-gitea is an operator-only, single-revision push broker.
// Never run it in an agent workspace or expose it as a tenant HTTP endpoint.
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
	var req prfactory.ReviewedPushRequest
	flag.StringVar(&req.RepositoryID, "repository-id", "", "explicit local repository ID")
	flag.StringVar(&req.ProjectID, "project-id", "", "explicit project ID")
	flag.StringVar(&req.OrganizationID, "organization-id", "", "explicit organization ID")
	flag.Int64Var(&req.UpstreamRepositoryID, "upstream-repository-id", 0, "verified upstream numeric repository ID")
	flag.Int64Var(&req.ExpectedGeneration, "expected-generation", 0, "current binding generation")
	flag.StringVar(&req.TaskID, "task-id", "", "task ID")
	flag.StringVar(&req.RunID, "run-id", "", "completed agent run ID")
	flag.StringVar(&req.ReviewID, "review-id", "", "approvable review report ID")
	flag.StringVar(&req.ApprovalID, "approval-id", "", "approved PR creation approval ID")
	flag.StringVar(&req.Branch, "branch", "", "exact approved agent branch")
	flag.StringVar(&req.BaseBranch, "base-branch", "", "exact approved base branch")
	flag.StringVar(&req.HeadSHA, "head", "", "independently reviewed full commit SHA")
	flag.StringVar(&req.BaseSHA, "base", "", "independently reviewed full base commit SHA")
	flag.StringVar(&req.BundlePath, "bundle", "", "operator-staged full Git bundle with refs/heads/export")
	apply := flag.Bool("apply", false, "operator confirms exact base/head, identity and destination after independent review")
	flag.Parse()
	if !*apply || flag.NArg() != 0 || req.BundlePath == "" {
		return errors.New("explicit reviewed bundle and --apply required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := prfactory.PublishReviewedGiteaBundle(ctx, database, req); err != nil {
		return err
	}
	fmt.Printf("Confirmed reviewed branch creation: repository=%s run=%s head=%s branch=%s\n", req.RepositoryID, req.RunID, req.HeadSHA, req.Branch)
	return nil
}
