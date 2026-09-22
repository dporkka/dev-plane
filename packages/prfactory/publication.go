package prfactory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

type projectPublicationVerifier struct {
	factory *Factory
	repoID  string
	runner  vcs.CommandRunner
}

func (v projectPublicationVerifier) Verify(ctx context.Context, workspacePath string) (vcs.VerificationReport, error) {
	commands, err := v.factory.loadVerificationCommands(ctx, v.repoID)
	if err != nil {
		return vcs.VerificationReport{}, err
	}
	return vcs.NewCommandVerifier(v.runner, commands).Verify(ctx, workspacePath)
}

func (f *Factory) prepareWorkspacePublication(
	ctx context.Context,
	repoID string,
	workspace *models.Workspace,
	targetBranch string,
	candidate vcs.VerifiedCandidate,
	remoteURL string,
) (vcs.PublicationOutcome, error) {
	runner, workspacePath, err := f.workspaceCommandRunner(ctx, workspace)
	if err != nil {
		return vcs.PublicationOutcome{}, err
	}
	verifier := projectPublicationVerifier{factory: f, repoID: repoID, runner: runner}
	return vcs.NewPublicationRefinery(runner, nil).Prepare(ctx, vcs.PublicationRequest{
		WorkspacePath: workspacePath,
		RemoteURL:     remoteURL,
		TargetBranch:  targetBranch,
		Candidate:     candidate,
		Verifier:      verifier,
		Env:           gitHubPushEnv(f.githubToken),
	})
}

func (f *Factory) workspaceCommandRunner(
	ctx context.Context,
	workspace *models.Workspace,
) (vcs.CommandRunner, string, error) {
	if workspace == nil {
		return nil, "", fmt.Errorf("workspace is required")
	}
	if workspace.WorktreePath != nil && strings.TrimSpace(*workspace.WorktreePath) != "" {
		return vcs.ExecRunner{}, strings.TrimSpace(*workspace.WorktreePath), nil
	}
	if workspace.RuntimeSessionID == nil || strings.TrimSpace(*workspace.RuntimeSessionID) == "" {
		return nil, "", fmt.Errorf("workspace has neither a host worktree nor a runtime session")
	}
	providerName := strings.ToLower(strings.TrimSpace(workspace.RuntimeProvider))
	provider := f.runtimeProviders[providerName]
	if provider == nil {
		return nil, "", fmt.Errorf("runtime provider %q is not registered for workspace verification", providerName)
	}
	if attacher, ok := provider.(interface {
		AttachSession(context.Context, string, string) (*runtimes.Session, error)
	}); ok {
		if _, err := attacher.AttachSession(ctx, *workspace.RuntimeSessionID, workspace.ID); err != nil {
			return nil, "", fmt.Errorf("attach runtime session: %w", err)
		}
	}
	return runtimes.NewVCSCommandRunner(provider, *workspace.RuntimeSessionID), ".", nil
}

func (f *Factory) loadVerificationCommands(ctx context.Context, repoID string) ([]vcs.VerificationCommand, error) {
	var lint, typecheck, test, build sql.NullString
	err := f.db.QueryRowContext(ctx, `
		SELECT lint_command, typecheck_command, test_command, build_command
		FROM project_configs
		WHERE repository_id = $1
	`, repoID).Scan(&lint, &typecheck, &test, &build)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load project verification commands: %w", err)
	}

	values := []struct {
		label string
		value sql.NullString
	}{
		{"lint", lint},
		{"typecheck", typecheck},
		{"test", test},
		{"build", build},
	}
	seen := map[string]struct{}{}
	commands := make([]vcs.VerificationCommand, 0, len(values))
	for _, item := range values {
		command := strings.TrimSpace(item.value.String)
		if !item.value.Valid || command == "" {
			continue
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		commands = append(commands, vcs.VerificationCommand{Label: item.label, Command: command})
	}
	return commands, nil
}

func validatePublicationOutcome(outcome vcs.PublicationOutcome, targetBranch string) error {
	switch outcome.Status {
	case vcs.PublicationTargetUnchanged, vcs.PublicationPrepared:
		return nil
	case vcs.PublicationAlreadyIntegrated:
		return fmt.Errorf("reviewed candidate is already integrated into target %s at %s", targetBranch, outcome.TargetHead)
	case vcs.PublicationConflict:
		return fmt.Errorf("reviewed candidate conflicts with target %s: %s", targetBranch, outcome.Detail)
	case vcs.PublicationRejected:
		return fmt.Errorf("reviewed candidate was rejected after target replay: %s", outcome.Detail)
	default:
		return fmt.Errorf("unexpected publication refinery status %q", outcome.Status)
	}
}
