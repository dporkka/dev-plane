package reviewer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/securityscan"
)

// A minimal SQL driver exercises the real Reviewer.Review entrypoint without
// adding a test-only database dependency or mocking review orchestration.
type reviewEvidenceFixture struct {
	workspacePath string
	missingPath   bool
	missingSteps  bool
	stepsError    bool
}

type reviewFixtureConnector struct{ fixture reviewEvidenceFixture }
type reviewFixtureDriver struct{}
type reviewFixtureConn struct{ fixture reviewEvidenceFixture }
type reviewFixtureRows struct {
	columns []string
	values  [][]driver.Value
}

func (c reviewFixtureConnector) Connect(context.Context) (driver.Conn, error) {
	return &reviewFixtureConn{fixture: c.fixture}, nil
}
func (c reviewFixtureConnector) Driver() driver.Driver { return reviewFixtureDriver{} }
func (reviewFixtureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}
func (*reviewFixtureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported in test fixture")
}
func (*reviewFixtureConn) Close() error { return nil }
func (*reviewFixtureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions unsupported")
}
func (*reviewFixtureConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (c *reviewFixtureConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	now := time.Now()
	switch {
	case strings.Contains(query, "FROM agent_runs"):
		return &reviewFixtureRows{
			columns: []string{"id", "task_id", "workspace_id", "agent_role", "model", "provider", "status", "prompt_tokens", "completion_tokens", "total_cost", "summary", "metadata", "created_at", "updated_at"},
			values:  [][]driver.Value{{"run-1", "task-1", "workspace-1", "implementer", "model", "provider", "completed", int64(1), int64(2), float64(0.1), "summary", []byte("{}"), now, now}},
		}, nil
	case strings.Contains(query, "FROM workspaces"):
		rows := [][]driver.Value{{c.fixture.workspacePath}}
		if c.fixture.missingPath {
			rows = nil
		}
		return &reviewFixtureRows{columns: []string{"worktree_path"}, values: rows}, nil
	case strings.Contains(query, "FROM agent_steps"):
		if c.fixture.stepsError {
			return nil, errors.New("agent step storage unavailable")
		}
		var rows [][]driver.Value
		if !c.fixture.missingSteps {
			rows = [][]driver.Value{{"step-1", "run-1", int64(1), "file_patch", "completed", nil, nil, nil, nil, nil, nil, nil, nil, nil, float64(0), int64(0), now}}
		}
		return &reviewFixtureRows{columns: []string{"id", "agent_run_id", "step_number", "step_type", "status", "content", "tool_name", "tool_input", "tool_output", "command", "command_output", "exit_code", "file_path", "diff", "cost", "latency_ms", "created_at"}, values: rows}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
}
func (r *reviewFixtureRows) Columns() []string { return r.columns }
func (r *reviewFixtureRows) Close() error      { return nil }
func (r *reviewFixtureRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func reviewFixtureRepo(t *testing.T, dirty bool) string {
	t.Helper()
	path := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Review Test"}, {"config", "user.email", "review@example.invalid"}} {
		if out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git setup: %s: %v", out, err)
		}
	}
	file := filepath.Join(path, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "main.go"}, {"commit", "-qm", "baseline"}} {
		if out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git commit: %s: %v", out, err)
		}
	}
	if dirty {
		if err := os.WriteFile(file, []byte("package main\nfunc example() {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestReviewRejectsUnavailableEvidence(t *testing.T) {
	changed := reviewFixtureRepo(t, true)
	clean := reviewFixtureRepo(t, false)
	cases := []struct {
		name    string
		fixture reviewEvidenceFixture
		want    string
	}{
		{"workspace missing", reviewEvidenceFixture{workspacePath: changed, missingPath: true}, "workspace"},
		{"diff command failed", reviewEvidenceFixture{workspacePath: filepath.Join(t.TempDir(), "missing")}, "git diff"},
		{"diff empty", reviewEvidenceFixture{workspacePath: clean}, "empty"},
		{"step query failed", reviewEvidenceFixture{workspacePath: changed, stepsError: true}, "agent steps"},
		{"no recorded steps", reviewEvidenceFixture{workspacePath: changed, missingSteps: true}, "agent steps"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(reviewFixtureConnector{fixture: tc.fixture})
			defer db.Close()
			reviewer := NewReviewer(db, nil).WithSecurityScanner(&fakeSecurityScanner{
				results: []*securityscan.ScanResult{{ScannerName: "gitleaks"}},
			})
			report, err := reviewer.Review(context.Background(), "run-1")
			if err == nil {
				t.Fatalf("expected review to reject missing evidence, got %+v", report)
			}
			if report != nil {
				t.Fatalf("unverified report returned: %+v", report)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestReviewAllowsCompleteInputs(t *testing.T) {
	db := sql.OpenDB(reviewFixtureConnector{fixture: reviewEvidenceFixture{workspacePath: reviewFixtureRepo(t, true)}})
	defer db.Close()
	reviewer := NewReviewer(db, nil).WithSecurityScanner(&fakeSecurityScanner{
		results: []*securityscan.ScanResult{{ScannerName: "gitleaks"}},
	})
	report, err := reviewer.Review(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("expected complete review to succeed: %v", err)
	}
	if report == nil || !report.Approvable {
		t.Fatalf("expected approvable complete report, got %+v", report)
	}
}
