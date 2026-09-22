package repogate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

func (g *Gate) loadWorkspaceContext(ctx context.Context, runID string) (workspaceContext, error) {
	var scope workspaceContext
	var workspaceID, worktreePath, runtimeProvider, runtimeSessionID sql.NullString
	var targetBranch, testCommand, lintCommand, typecheckCommand, buildCommand sql.NullString
	err := g.db.QueryRowContext(ctx, `
		SELECT ar.id, ar.task_id, ar.workspace_id, ar.agent_role,
		       t.repository_id, w.branch, w.base_branch, w.worktree_path,
		       w.runtime_provider, w.runtime_session_id,
		       t.target_branch, rr.approvable,
		       pc.test_command, pc.lint_command, pc.typecheck_command, pc.build_command
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id AND t.deleted_at IS NULL
		JOIN workspaces w ON w.id = ar.workspace_id AND w.deleted_at IS NULL
		JOIN review_reports rr ON rr.run_id = ar.id
		LEFT JOIN project_configs pc ON pc.id = (
			SELECT pc2.id
			FROM project_configs pc2
			WHERE pc2.repository_id = t.repository_id
			ORDER BY pc2.updated_at DESC
			LIMIT 1
		)
		WHERE ar.id = $1
	`, runID).Scan(
		&scope.runID, &scope.taskID, &workspaceID, &scope.agentRole,
		&scope.repositoryID, &scope.sourceBranch, &scope.baseBranch, &worktreePath,
		&runtimeProvider, &runtimeSessionID, &targetBranch, &scope.approvable,
		&testCommand, &lintCommand, &typecheckCommand, &buildCommand,
	)
	if err != nil {
		return workspaceContext{}, fmt.Errorf("load repository gate context: %w", err)
	}
	if !workspaceID.Valid || strings.TrimSpace(workspaceID.String) == "" {
		return workspaceContext{}, fmt.Errorf("run %s has no workspace", runID)
	}
	scope.workspaceID = workspaceID.String
	scope.worktreePath = strings.TrimSpace(worktreePath.String)
	scope.runtimeProvider = strings.ToLower(strings.TrimSpace(runtimeProvider.String))
	scope.runtimeSessionID = strings.TrimSpace(runtimeSessionID.String)
	scope.targetBranch = strings.TrimSpace(targetBranch.String)
	if scope.targetBranch == "" {
		scope.targetBranch = strings.TrimSpace(scope.baseBranch)
	}
	scope.testCommand = strings.TrimSpace(testCommand.String)
	scope.lintCommand = strings.TrimSpace(lintCommand.String)
	scope.typecheckCommand = strings.TrimSpace(typecheckCommand.String)
	scope.buildCommand = strings.TrimSpace(buildCommand.String)
	return scope, nil
}

func (g *Gate) commandRunner(ctx context.Context, scope workspaceContext) (vcs.CommandRunner, string, error) {
	if scope.runtimeSessionID != "" {
		provider := g.providers[scope.runtimeProvider]
		if provider == nil {
			return nil, "", fmt.Errorf("runtime provider %q is not registered for repository gate", scope.runtimeProvider)
		}
		if attacher, ok := provider.(runtimes.SessionAttacher); ok {
			if _, err := attacher.AttachSession(ctx, scope.runtimeSessionID, scope.workspaceID); err != nil {
				return nil, "", fmt.Errorf("attach runtime session: %w", err)
			}
		}
		return runtimes.NewVCSCommandRunner(provider, scope.runtimeSessionID), ".", nil
	}
	if scope.worktreePath == "" {
		return nil, "", fmt.Errorf("workspace %s has neither a runtime session nor host worktree", scope.workspaceID)
	}
	return vcs.ExecRunner{}, scope.worktreePath, nil
}

func validationCommands(scope workspaceContext) []string {
	commands := []string{scope.testCommand, scope.lintCommand, scope.typecheckCommand, scope.buildCommand}
	out := make([]string, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		out = append(out, command)
	}
	return out
}

func integrationBranch(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	taskID = strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(taskID)
	return "dev-plane/integrated/" + taskID
}
