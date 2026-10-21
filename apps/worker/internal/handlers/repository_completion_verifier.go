package handlers

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	storedb "github.com/ai-dev-control-plane/db"
	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/verifier"
)

// RepositoryCompletionVerifier uses the persisted WorkItem and provisioned
// workspace, not agent-provided revision IDs, to verify the completed candidate.
// WorkItem provisioning is a prerequisite: missing work or base evidence fails.
type RepositoryCompletionVerifier struct {
	store   *storedb.DB
	runtime runtimes.Provider
}

func NewRepositoryCompletionVerifier(store *storedb.DB, runtime runtimes.Provider) *RepositoryCompletionVerifier {
	return &RepositoryCompletionVerifier{store: store, runtime: runtime}
}

func (v *RepositoryCompletionVerifier) VerifyCompletedRun(ctx context.Context, runID, taskID string) (CompletionVerification, error) {
	if v == nil || v.store == nil || v.DB() == nil || v.runtime == nil {
		return CompletionVerification{}, errors.New("repository verification dependencies are unavailable")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(taskID) == "" {
		return CompletionVerification{}, errors.New("run and task IDs are required for verification")
	}
	var persistedTaskID, status string
	var workspaceTaskID, sessionID sql.NullString
	var settings []byte
	err := v.store.QueryRowContext(ctx, `
		SELECT ar.task_id, ar.status, w.task_id, w.runtime_session_id, w.settings
		FROM agent_runs ar
		JOIN workspaces w ON w.id = ar.workspace_id
		WHERE ar.id = $1
	`, runID).Scan(&persistedTaskID, &status, &workspaceTaskID, &sessionID, &settings)
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("load run workspace verification authority: %w", err)
	}
	if persistedTaskID != taskID || !workspaceTaskID.Valid || workspaceTaskID.String != taskID {
		return CompletionVerification{}, errors.New("run/workspace/task association does not match")
	}
	if status != "completed" {
		return CompletionVerification{}, fmt.Errorf("run %s status %q is not completed", runID, status)
	}
	if !sessionID.Valid || strings.TrimSpace(sessionID.String) == "" {
		return CompletionVerification{}, errors.New("run workspace lacks an executable runtime session")
	}
	var workspaceSettings struct {
		BaseSHA string `json:"base_sha"`
	}
	if err := json.Unmarshal(settings, &workspaceSettings); err != nil {
		return CompletionVerification{}, fmt.Errorf("decode immutable workspace base: %w", err)
	}
	base := strings.TrimSpace(workspaceSettings.BaseSHA)
	if !validGitRevision(base) {
		return CompletionVerification{}, errors.New("workspace lacks valid immutable base revision")
	}
	item, err := v.store.GetWorkItem(ctx, taskID)
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("load required durable work item: %w", err)
	}
	if item.ID != taskID || item.BaseSHA != base {
		return CompletionVerification{}, errors.New("work item base does not match provisioned workspace")
	}
	if err := item.Validate(); err != nil {
		return CompletionVerification{}, fmt.Errorf("work item invalid: %w", err)
	}
	switch item.State {
	case repoprotocol.WorkImplementing, repoprotocol.WorkVerifying, repoprotocol.WorkReviewing, repoprotocol.WorkReadyToLand:
	default:
		return CompletionVerification{}, fmt.Errorf("work item in invalid verification state %q", item.State)
	}
	// A reserved human approval or reviewer gate cannot substitute for executing
	// an actual independent test/check command.
	hasExecutable := false
	hasIndependentReview := false
	for _, name := range item.RequiredGates {
		if name == repoprotocol.GateIndependentReview {
			hasIndependentReview = true
		}
		if name != repoprotocol.GateIndependentReview && name != repoprotocol.GateHumanApproval && strings.TrimSpace(name) != "" {
			hasExecutable = true
		}
	}
	if !hasExecutable {
		return CompletionVerification{}, errors.New("work item requires at least one executable verification gate")
	}
	if !hasIndependentReview {
		return CompletionVerification{}, errors.New("work item must require independent review before landing")
	}
	session := sessionID.String
	head, err := v.gitOutput(ctx, session, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return CompletionVerification{}, err
	}
	head = strings.TrimSpace(head)
	if !validGitRevision(head) || head == base {
		return CompletionVerification{}, errors.New("candidate head is invalid or unchanged from immutable base")
	}
	if _, err := v.gitOutput(ctx, session, "merge-base", "--is-ancestor", base, head); err != nil {
		return CompletionVerification{}, fmt.Errorf("candidate head is not based on immutable base: %w", err)
	}
	changed, err := v.gitOutput(ctx, session, "diff", "--name-only", "-z", "--no-renames", base, head, "--")
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("get changed paths: %w", err)
	}
	if changed == "" {
		return CompletionVerification{}, errors.New("candidate does not change any tracked paths")
	}
	paths := strings.Split(strings.TrimSuffix(changed, "\x00"), "\x00")
	for _, path := range paths {
		if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\x00") ||
			path == ".." || strings.HasPrefix(path, "../") {
			return CompletionVerification{}, fmt.Errorf("invalid changed path %q", path)
		}
	}
	if err := v.assertClean(ctx, session); err != nil {
		return CompletionVerification{}, err
	}
	result, err := verifier.New(v.runtime, v.store).Verify(ctx, verifier.Request{
		SessionID: session, WorkItem: item, CandidateHead: head, ChangedPaths: paths,
	})
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("independent candidate verification failed: %w", err)
	}
	if result.Evidence.BaseSHA != base {
		return CompletionVerification{}, errors.New("verifier returned different immutable base")
	}
	if err := result.Evidence.ValidateHead(head); err != nil {
		return CompletionVerification{}, fmt.Errorf("verifier did not produce passing exact-head evidence: %w", err)
	}
	if err := v.assertClean(ctx, session); err != nil {
		return CompletionVerification{}, err
	}
	currentHead, err := v.gitOutput(ctx, session, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return CompletionVerification{}, err
	}
	if strings.TrimSpace(currentHead) != head {
		return CompletionVerification{}, errors.New("candidate head changed after independent verification")
	}
	return CompletionVerification{BaseSHA: base, HeadSHA: head}, nil
}

func (v *RepositoryCompletionVerifier) DB() *sql.DB {
	if v == nil || v.store == nil {
		return nil
	}
	return v.store.DB
}

func (v *RepositoryCompletionVerifier) gitOutput(ctx context.Context, session string, args ...string) (string, error) {
	command := runtimes.Command{Args: append([]string{"git"}, args...), Timeout: 30 * time.Second}
	output, err := v.runtime.ExecuteCommand(ctx, session, command)
	if err != nil {
		return "", fmt.Errorf("execute git %s: %w", strings.Join(args, " "), err)
	}
	if output == nil || output.ExitCode != 0 {
		return "", fmt.Errorf("git %s failed", strings.Join(args, " "))
	}
	return output.Stdout, nil
}

func (v *RepositoryCompletionVerifier) assertClean(ctx context.Context, session string) error {
	status, err := v.gitOutput(ctx, session, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("workspace has uncommitted changes during verification")
	}
	return nil
}

func validGitRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
