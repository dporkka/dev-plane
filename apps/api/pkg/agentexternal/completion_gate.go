package agentexternal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/runtimes"
)

const (
	defaultCompletionCheckTimeout = 5 * time.Minute
	maxCompletionCheckTimeout     = 30 * time.Minute
	headCheckTimeout              = 30 * time.Second
)

// CompletionVerificationCheck is one trusted repository verification command.
// Commands are resolved by control-plane configuration rather than provider text.
type CompletionVerificationCheck struct {
	Name    string
	Command string
	Timeout time.Duration
}

// CompletionVerificationPlanResolver resolves the checks required before an
// external provider turn may cross Dev Plane's completion boundary.
type CompletionVerificationPlanResolver interface {
	ResolveCompletionVerification(ctx context.Context, workspaceID, sessionID string) ([]CompletionVerificationCheck, error)
}

// CompletionCommandRuntime is the workspace execution subset required by the
// exact-HEAD completion gate.
type CompletionCommandRuntime interface {
	ExecuteCommand(ctx context.Context, sessionID string, command runtimes.Command) (*runtimes.CommandResult, error)
}

// ExactHeadCompletionGate runs trusted verification commands against the
// workspace runtime and proves that HEAD remains identical throughout the
// verification sequence.
type ExactHeadCompletionGate struct {
	db      *sql.DB
	runtime CompletionCommandRuntime
	plans   CompletionVerificationPlanResolver
}

// NewExactHeadCompletionGate creates a fail-closed completion gate.
func NewExactHeadCompletionGate(db *sql.DB, runtime CompletionCommandRuntime, plans CompletionVerificationPlanResolver) (*ExactHeadCompletionGate, error) {
	if db == nil {
		return nil, errors.New("completion gate database is required")
	}
	if runtime == nil {
		return nil, errors.New("completion gate runtime is required")
	}
	if plans == nil {
		return nil, errors.New("completion verification plan resolver is required")
	}
	return &ExactHeadCompletionGate{db: db, runtime: runtime, plans: plans}, nil
}

// VerifyExternalRunCompletion implements CompletionGate. Provider success is
// insufficient by itself: the workspace must have a live runtime session, a
// non-empty trusted verification plan, all checks must pass, and the exact Git
// HEAD observed before verification must remain unchanged after every check.
func (g *ExactHeadCompletionGate) VerifyExternalRunCompletion(ctx context.Context, req CompletionRequest) error {
	if g == nil || g.db == nil || g.runtime == nil || g.plans == nil {
		return errors.New("exact-head completion gate is not configured")
	}
	if err := validateCompletionRequest(req); err != nil {
		return err
	}

	sessionID, err := g.loadRuntimeSession(ctx, req.WorkspaceID)
	if err != nil {
		return err
	}
	checks, err := g.plans.ResolveCompletionVerification(ctx, req.WorkspaceID, sessionID)
	if err != nil {
		return fmt.Errorf("resolve completion verification plan: %w", err)
	}
	if len(checks) == 0 {
		return errors.New("completion verification plan is empty")
	}

	expectedHead, err := g.readHead(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		command := strings.TrimSpace(check.Command)
		if name == "" {
			return errors.New("completion verification check name is required")
		}
		if command == "" {
			return fmt.Errorf("completion verification check %s command is required", name)
		}

		result, runErr := g.runtime.ExecuteCommand(ctx, sessionID, runtimes.Command{
			Command:     command,
			Timeout:     completionCheckTimeout(check.Timeout),
			UnsafeShell: true,
		})
		if runErr != nil {
			return fmt.Errorf("run completion verification check %s: %w", name, runErr)
		}
		if result == nil {
			return fmt.Errorf("completion verification check %s returned no result", name)
		}
		if result.ExitCode != 0 {
			output := strings.TrimSpace(result.Stderr)
			if output == "" {
				output = strings.TrimSpace(result.Stdout)
			}
			if output == "" {
				return fmt.Errorf("completion verification check %s failed with exit code %d", name, result.ExitCode)
			}
			return fmt.Errorf("completion verification check %s failed with exit code %d: %s", name, result.ExitCode, output)
		}

		actualHead, err := g.readHead(ctx, sessionID)
		if err != nil {
			return err
		}
		if actualHead != expectedHead {
			return fmt.Errorf("workspace head changed during completion verification: expected=%s actual=%s", expectedHead, actualHead)
		}
	}
	return nil
}

func (g *ExactHeadCompletionGate) loadRuntimeSession(ctx context.Context, workspaceID string) (string, error) {
	var session sql.NullString
	if err := g.db.QueryRowContext(ctx, `
		SELECT runtime_session_id
		FROM workspaces
		WHERE id = $1 AND deleted_at IS NULL
	`, workspaceID).Scan(&session); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("workspace %s not found", workspaceID)
		}
		return "", fmt.Errorf("load workspace runtime session: %w", err)
	}
	sessionID := strings.TrimSpace(session.String)
	if !session.Valid || sessionID == "" {
		return "", fmt.Errorf("workspace %s has no runtime session", workspaceID)
	}
	return sessionID, nil
}

func (g *ExactHeadCompletionGate) readHead(ctx context.Context, sessionID string) (string, error) {
	result, err := g.runtime.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Args:    []string{"git", "rev-parse", "HEAD"},
		Timeout: headCheckTimeout,
	})
	if err != nil {
		return "", fmt.Errorf("read workspace head: %w", err)
	}
	if result == nil {
		return "", errors.New("read workspace head returned no result")
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("read workspace head failed with exit code %d", result.ExitCode)
	}
	head := strings.TrimSpace(result.Stdout)
	if head == "" {
		return "", errors.New("workspace head is empty")
	}
	return head, nil
}

func validateCompletionRequest(req CompletionRequest) error {
	fields := []struct {
		name  string
		value string
	}{
		{"run id", req.RunID},
		{"task id", req.TaskID},
		{"workspace id", req.WorkspaceID},
		{"thread id", req.ThreadID},
		{"turn id", req.TurnID},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("completion %s is required", field.name)
		}
	}
	return nil
}

func completionCheckTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultCompletionCheckTimeout
	}
	if timeout > maxCompletionCheckTimeout {
		return maxCompletionCheckTimeout
	}
	return timeout
}

var _ CompletionGate = (*ExactHeadCompletionGate)(nil)
