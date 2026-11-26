// Command forge-check-gitea-ci binds trusted verification work and checks
// signed exact-head CI evidence. It never authorizes or performs a merge.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/prfactory"
	rp "github.com/ai-dev-control-plane/repoprotocol"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

type operatorPolicy struct {
	PullRequestID  string                       `json:"pull_request_id"`
	RepositoryID   string                       `json:"repository_id"`
	ProjectID      string                       `json:"project_id"`
	OrganizationID string                       `json:"organization_id"`
	HeadSHA        string                       `json:"head_sha"`
	BaseSHA        string                       `json:"base_sha"`
	Generation     int64                        `json:"generation"`
	WorkItemID     string                       `json:"work_item_id"`
	Gates          map[string]rp.RemoteCIPolicy `json:"gates"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readPolicy(path string) (operatorPolicy, error) {
	var p operatorPolicy
	fail := errors.New("trusted CI policy file missing or invalid")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 1<<20 {
		return p, fail
	}
	file, err := os.Open(path)
	if err != nil {
		return p, fail
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return p, fail
	}
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fail
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || p.WorkItemID == "" || len(p.Gates) == 0 || len(p.Gates) > 64 {
		return p, fail
	}
	return p, nil
}

func run() error {
	path := flag.String("policy", "", "trusted operator-managed release policy JSON (outside agent workspace)")
	bind := flag.Bool("bind", false, "explicitly bind this work item and gate set; otherwise perform read-only verification")
	flag.Parse()
	if flag.NArg() != 0 || *path == "" {
		return errors.New("explicit --policy required")
	}
	p, err := readPolicy(*path)
	if err != nil {
		return err
	}
	req := prfactory.GiteaMergePreflightRequest{PullRequestID: p.PullRequestID, RepositoryID: p.RepositoryID, ProjectID: p.ProjectID, OrganizationID: p.OrganizationID, ExpectedHeadSHA: p.HeadSHA, ExpectedBaseSHA: p.BaseSHA, ExpectedGeneration: p.Generation}
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if *bind {
		names := make([]string, 0, len(p.Gates))
		for name := range p.Gates {
			names = append(names, name)
		}
		if err := prfactory.BindGiteaCIWorkItem(ctx, database, req, p.WorkItemID, names); err != nil {
			return err
		}
		fmt.Println("Trusted CI work-item binding created; no merge authorized.")
		return nil
	}
	if err := prfactory.CheckGiteaCIForWorkItem(ctx, database, req, p.WorkItemID, p.Gates); err != nil {
		return err
	}
	fmt.Println("Signed exact-head CI evidence verified. Not merge authorization.")
	return nil
}
