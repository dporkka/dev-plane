package agentrunner

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/projectbrain"
	"github.com/ai-dev-control-plane/runtimes"
)

const projectContextMetadataKey = "project_context"

// ProjectContextProvider compiles immutable, task-specific repository context.
// projectbrain.Compiler satisfies this interface directly.
type ProjectContextProvider interface {
	Compile(context.Context, projectbrain.CompileRequest) (projectbrain.ContextPackage, error)
}

// WithProjectContextProvider enables Project Brain context for agent runs.
// When no provider is configured, the legacy runner behavior is unchanged.
func (r *Runner) WithProjectContextProvider(provider ProjectContextProvider) *Runner {
	if r != nil {
		r.contextProvider = provider
	}
	return r
}

func (r *Runner) prepareProjectContext(
	ctx context.Context,
	run *models.AgentRun,
	task *models.Task,
	workspace *models.Workspace,
	workspacePath string,
) (*projectbrain.ContextPackage, error) {
	if r == nil || r.contextProvider == nil {
		return nil, nil
	}
	if run == nil || strings.TrimSpace(run.ID) == "" {
		return nil, errors.New("project context requires agent run identity")
	}
	if r.db == nil {
		return nil, errors.New("project context requires durable agent run storage")
	}

	revision, err := r.workspaceHeadCommitRevision(ctx, workspace, workspacePath)
	if err != nil {
		return nil, fmt.Errorf("capture project context revision: %w", err)
	}
	req, err := projectContextRequest(task, revision)
	if err != nil {
		return nil, err
	}
	var pkg projectbrain.ContextPackage
	if workspaceProvider, ok := r.contextProvider.(WorkspaceProjectContextProvider); ok {
		pkg, err = workspaceProvider.CompileWorkspace(ctx, req, workspacePath)
	} else {
		pkg, err = r.contextProvider.Compile(ctx, req)
	}
	if err != nil {
		return nil, fmt.Errorf("compile project context: %w", err)
	}
	if pkg.Version != projectbrain.ContextPackageVersion {
		return nil, fmt.Errorf("project context version=%d, want %d", pkg.Version, projectbrain.ContextPackageVersion)
	}
	if pkg.Repository != req.Repository {
		return nil, fmt.Errorf("project context repository mismatch: package=%s request=%s", pkg.Repository, req.Repository)
	}
	if pkg.Revision != req.Revision {
		return nil, fmt.Errorf("project context revision mismatch: package=%s request=%s", pkg.Revision, req.Revision)
	}
	if strings.TrimSpace(pkg.Digest) == "" {
		return nil, errors.New("project context digest is required")
	}

	metadata, err := mergeProjectContextMetadata(run.Metadata, pkg)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET metadata = $1, updated_at = $2
		WHERE id = $3
	`, string(metadata), now, run.ID)
	if err != nil {
		return nil, fmt.Errorf("persist project context for run %s: %w", run.ID, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows != 1 {
		return nil, fmt.Errorf("persist project context for run %s affected %d rows", run.ID, rows)
	}
	run.Metadata = metadata
	return &pkg, nil
}

func projectContextRequest(task *models.Task, revision string) (projectbrain.CompileRequest, error) {
	if task == nil {
		return projectbrain.CompileRequest{}, errors.New("project context task is required")
	}
	repository := strings.TrimSpace(task.RepositoryID)
	if repository == "" {
		return projectbrain.CompileRequest{}, errors.New("project context repository is required")
	}
	revision = strings.TrimSpace(revision)
	if !strings.HasPrefix(revision, "git-commit:") || strings.TrimSpace(strings.TrimPrefix(revision, "git-commit:")) == "" {
		return projectbrain.CompileRequest{}, fmt.Errorf("project context requires git-commit revision, got %q", revision)
	}

	objective := strings.TrimSpace(task.Title)
	if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
		if objective != "" {
			objective += "\n"
		}
		objective += strings.TrimSpace(*task.Description)
	}
	if objective == "" {
		return projectbrain.CompileRequest{}, errors.New("project context objective is required")
	}

	criteria := make([]string, 0)
	if len(task.AcceptanceCriteria) > 0 && string(task.AcceptanceCriteria) != "null" {
		var taskCriteria []string
		if err := json.Unmarshal(task.AcceptanceCriteria, &taskCriteria); err != nil {
			return projectbrain.CompileRequest{}, fmt.Errorf("decode task acceptance criteria: %w", err)
		}
		criteria = appendUniqueStrings(criteria, taskCriteria...)
	}

	var spec struct {
		FilesToChange      []string `json:"files_to_change"`
		FilesToCreate      []string `json:"files_to_create"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	if len(task.Spec) > 0 && string(task.Spec) != "null" {
		if err := json.Unmarshal(task.Spec, &spec); err != nil {
			return projectbrain.CompileRequest{}, fmt.Errorf("decode task spec for project context: %w", err)
		}
		criteria = appendUniqueStrings(criteria, spec.AcceptanceCriteria...)
	}
	changedPaths := appendUniqueStrings(nil, spec.FilesToChange...)
	changedPaths = appendUniqueStrings(changedPaths, spec.FilesToCreate...)

	return projectbrain.CompileRequest{
		Repository:         repository,
		Revision:           revision,
		Objective:          objective,
		AcceptanceCriteria: criteria,
		ChangedPaths:       changedPaths,
	}, nil
}

func mergeProjectContextMetadata(existing json.RawMessage, pkg projectbrain.ContextPackage) (json.RawMessage, error) {
	metadata := make(map[string]json.RawMessage)
	trimmed := strings.TrimSpace(string(existing))
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(existing, &metadata); err != nil {
			return nil, fmt.Errorf("decode agent run metadata before project context merge: %w", err)
		}
	}
	encoded, err := json.Marshal(pkg)
	if err != nil {
		return nil, fmt.Errorf("encode project context: %w", err)
	}
	metadata[projectContextMetadataKey] = encoded
	merged, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode agent run metadata with project context: %w", err)
	}
	return merged, nil
}

func appendUniqueStrings(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(values))
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		existing = append(existing, value)
	}
	return existing
}

func (r *Runner) workspaceHeadCommitRevision(ctx context.Context, workspace *models.Workspace, workspacePath string) (string, error) {
	provider, sessionID, err := r.runtimeProviderForWorkspace(ctx, workspace)
	if err != nil {
		return "", err
	}
	if provider != nil {
		result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
			Args:    []string{"git", "rev-parse", "--verify", "HEAD^{commit}"},
			Timeout: 30 * time.Second,
		})
		if err != nil {
			return "", err
		}
		if result == nil {
			return "", errors.New("workspace HEAD command returned no result")
		}
		if result.ExitCode != 0 {
			return "", fmt.Errorf("workspace HEAD command failed: %s", strings.TrimSpace(result.Stdout+result.Stderr))
		}
		return canonicalGitCommitRevision(result.Stdout)
	}

	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", errors.New("workspace path is required")
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD^{commit}")
	cmd.Dir = workspacePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return canonicalGitCommitRevision(string(output))
}

func canonicalGitCommitRevision(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 40 && len(value) != 64 {
		return "", fmt.Errorf("git commit hash has invalid length %d", len(value))
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("git commit hash is not hexadecimal: %w", err)
	}
	return "git-commit:" + value, nil
}

func projectContextWarningSources(warnings []projectbrain.Warning) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, warning := range warnings {
		source := strings.TrimSpace(warning.Source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		result = append(result, source)
	}
	sort.Strings(result)
	return result
}
