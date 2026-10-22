package agentexternal

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/runtimes"
)

type completionRuntimeCall struct {
	sessionID string
	command   runtimes.Command
}

type fakeCompletionRuntime struct {
	calls     []completionRuntimeCall
	responses []*runtimes.CommandResult
	errors    []error
}

func (r *fakeCompletionRuntime) ExecuteCommand(_ context.Context, sessionID string, command runtimes.Command) (*runtimes.CommandResult, error) {
	r.calls = append(r.calls, completionRuntimeCall{sessionID: sessionID, command: command})
	index := len(r.calls) - 1
	var result *runtimes.CommandResult
	var err error
	if index < len(r.responses) {
		result = r.responses[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return result, err
}

type fakeVerificationPlanResolver struct {
	checks      []CompletionVerificationCheck
	err         error
	workspaceID string
	sessionID   string
}

func (r *fakeVerificationPlanResolver) ResolveCompletionVerification(_ context.Context, workspaceID, sessionID string) ([]CompletionVerificationCheck, error) {
	r.workspaceID = workspaceID
	r.sessionID = sessionID
	if r.err != nil {
		return nil, r.err
	}
	return append([]CompletionVerificationCheck(nil), r.checks...), nil
}

func newCompletionGateDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func validCompletionRequest() CompletionRequest {
	return CompletionRequest{
		RunID:       "run-1",
		TaskID:      "task-1",
		WorkspaceID: "workspace-1",
		ThreadID:    "thread-1",
		TurnID:      "turn-1",
	}
}

func TestExactHeadCompletionGateRunsResolvedChecksAgainstStableHead(t *testing.T) {
	db, mock := newCompletionGateDB(t)
	mock.ExpectQuery(`SELECT runtime_session_id`).
		WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_session_id"}).AddRow("session-1"))

	runtime := &fakeCompletionRuntime{responses: []*runtimes.CommandResult{
		{ExitCode: 0, Stdout: "abc123\n"},
		{ExitCode: 0, Stdout: "tests passed"},
		{ExitCode: 0, Stdout: "abc123\n"},
		{ExitCode: 0, Stdout: "lint passed"},
		{ExitCode: 0, Stdout: "abc123\n"},
	}}
	plans := &fakeVerificationPlanResolver{checks: []CompletionVerificationCheck{
		{Name: "test", Command: "make test", Timeout: 3 * time.Minute},
		{Name: "lint", Command: "make lint", Timeout: 2 * time.Minute},
	}}
	gate, err := NewExactHeadCompletionGate(db, runtime, plans)
	if err != nil {
		t.Fatalf("NewExactHeadCompletionGate() error = %v", err)
	}

	if err := gate.VerifyExternalRunCompletion(context.Background(), validCompletionRequest()); err != nil {
		t.Fatalf("VerifyExternalRunCompletion() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
	if plans.workspaceID != "workspace-1" || plans.sessionID != "session-1" {
		t.Fatalf("resolver ownership = %q/%q", plans.workspaceID, plans.sessionID)
	}

	if len(runtime.calls) != 5 {
		t.Fatalf("runtime calls = %d, want 5", len(runtime.calls))
	}
	for _, index := range []int{0, 2, 4} {
		if !reflect.DeepEqual(runtime.calls[index].command.Args, []string{"git", "rev-parse", "HEAD"}) {
			t.Fatalf("head check %d args = %#v", index, runtime.calls[index].command.Args)
		}
	}
	if runtime.calls[1].command.Command != "make test" || !runtime.calls[1].command.UnsafeShell {
		t.Fatalf("test command = %#v", runtime.calls[1].command)
	}
	if runtime.calls[3].command.Command != "make lint" || !runtime.calls[3].command.UnsafeShell {
		t.Fatalf("lint command = %#v", runtime.calls[3].command)
	}
}

func TestExactHeadCompletionGateRejectsChangedHead(t *testing.T) {
	db, mock := newCompletionGateDB(t)
	mock.ExpectQuery(`SELECT runtime_session_id`).WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_session_id"}).AddRow("session-1"))
	runtime := &fakeCompletionRuntime{responses: []*runtimes.CommandResult{
		{ExitCode: 0, Stdout: "abc123\n"},
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "def456\n"},
	}}
	plans := &fakeVerificationPlanResolver{checks: []CompletionVerificationCheck{{Name: "test", Command: "make test"}}}
	gate, err := NewExactHeadCompletionGate(db, runtime, plans)
	if err != nil {
		t.Fatalf("NewExactHeadCompletionGate() error = %v", err)
	}

	err = gate.VerifyExternalRunCompletion(context.Background(), validCompletionRequest())
	if err == nil || err.Error() != "workspace head changed during completion verification: expected=abc123 actual=def456" {
		t.Fatalf("VerifyExternalRunCompletion() error = %v", err)
	}
}

func TestExactHeadCompletionGateRejectsFailedCheck(t *testing.T) {
	db, mock := newCompletionGateDB(t)
	mock.ExpectQuery(`SELECT runtime_session_id`).WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_session_id"}).AddRow("session-1"))
	runtime := &fakeCompletionRuntime{responses: []*runtimes.CommandResult{
		{ExitCode: 0, Stdout: "abc123\n"},
		{ExitCode: 2, Stderr: "test failure"},
	}}
	plans := &fakeVerificationPlanResolver{checks: []CompletionVerificationCheck{{Name: "test", Command: "make test"}}}
	gate, err := NewExactHeadCompletionGate(db, runtime, plans)
	if err != nil {
		t.Fatalf("NewExactHeadCompletionGate() error = %v", err)
	}

	err = gate.VerifyExternalRunCompletion(context.Background(), validCompletionRequest())
	if err == nil || err.Error() != "completion verification check test failed with exit code 2: test failure" {
		t.Fatalf("VerifyExternalRunCompletion() error = %v", err)
	}
	if len(runtime.calls) != 2 {
		t.Fatalf("runtime calls = %d, want 2", len(runtime.calls))
	}
}

func TestExactHeadCompletionGateFailsClosedWithoutVerificationPlan(t *testing.T) {
	db, mock := newCompletionGateDB(t)
	mock.ExpectQuery(`SELECT runtime_session_id`).WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_session_id"}).AddRow("session-1"))
	gate, err := NewExactHeadCompletionGate(db, &fakeCompletionRuntime{}, &fakeVerificationPlanResolver{})
	if err != nil {
		t.Fatalf("NewExactHeadCompletionGate() error = %v", err)
	}

	err = gate.VerifyExternalRunCompletion(context.Background(), validCompletionRequest())
	if err == nil || err.Error() != "completion verification plan is empty" {
		t.Fatalf("VerifyExternalRunCompletion() error = %v", err)
	}
}

func TestExactHeadCompletionGateFailsClosedWithoutRuntimeSession(t *testing.T) {
	db, mock := newCompletionGateDB(t)
	mock.ExpectQuery(`SELECT runtime_session_id`).WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_session_id"}).AddRow(nil))
	gate, err := NewExactHeadCompletionGate(db, &fakeCompletionRuntime{}, &fakeVerificationPlanResolver{checks: []CompletionVerificationCheck{{Name: "test", Command: "make test"}}})
	if err != nil {
		t.Fatalf("NewExactHeadCompletionGate() error = %v", err)
	}

	err = gate.VerifyExternalRunCompletion(context.Background(), validCompletionRequest())
	if err == nil || err.Error() != "workspace workspace-1 has no runtime session" {
		t.Fatalf("VerifyExternalRunCompletion() error = %v", err)
	}
}

func TestExactHeadCompletionGatePropagatesRuntimeError(t *testing.T) {
	db, mock := newCompletionGateDB(t)
	mock.ExpectQuery(`SELECT runtime_session_id`).WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"runtime_session_id"}).AddRow("session-1"))
	runtime := &fakeCompletionRuntime{
		responses: []*runtimes.CommandResult{{ExitCode: 0, Stdout: "abc123\n"}, nil},
		errors:    []error{nil, errors.New("runner unavailable")},
	}
	plans := &fakeVerificationPlanResolver{checks: []CompletionVerificationCheck{{Name: "test", Command: "make test"}}}
	gate, err := NewExactHeadCompletionGate(db, runtime, plans)
	if err != nil {
		t.Fatalf("NewExactHeadCompletionGate() error = %v", err)
	}

	err = gate.VerifyExternalRunCompletion(context.Background(), validCompletionRequest())
	if err == nil || err.Error() != "run completion verification check test: runner unavailable" {
		t.Fatalf("VerifyExternalRunCompletion() error = %v", err)
	}
}
