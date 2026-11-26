package prfactory

import (
	"context"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

type giteaPRRevision struct {
	approvalID, headSHA, baseSHA string
	upstreamRepositoryID         int64
}

// Validate the live identity and the exact original approval/review snapshot.
// A receipt by itself is not current authority.
const giteaPRRevisionFrom = `
 FROM gitea_revision_attestations a
 JOIN gitea_publication_receipts rc ON rc.approval_id=a.approval_id AND rc.observed_head_sha=a.head_sha
 JOIN tasks t ON t.id=a.task_id AND t.repository_id=a.repository_id AND t.project_id=a.project_id AND t.workspace_id=a.workspace_id
 JOIN projects p ON p.id=a.project_id AND p.organization_id=a.organization_id AND p.deleted_at IS NULL
 JOIN organizations o ON o.id=a.organization_id AND o.deleted_at IS NULL
 JOIN repositories r ON r.id=a.repository_id AND r.project_id=p.id AND r.deleted_at IS NULL
 JOIN gitea_repository_bindings b ON b.repository_id=r.id AND b.project_id=p.id AND b.organization_id=o.id
  AND b.full_name=r.full_name AND b.instance_url=a.instance_url AND b.upstream_repository_id=a.upstream_repository_id
  AND b.authorization_generation=a.authorization_generation AND b.revoked_at IS NULL
 JOIN agent_runs ar ON ar.id=a.run_id AND ar.task_id=t.id AND ar.workspace_id=a.workspace_id AND ar.status IN ('completed','reviewed')
 JOIN workspaces w ON w.id=a.workspace_id AND w.task_id=t.id AND w.repository_id=r.id AND w.deleted_at IS NULL
  AND w.status='ready' AND w.runtime_provider='docker' AND (w.worktree_path IS NULL OR w.worktree_path='')
 JOIN review_reports rr ON rr.id=a.review_id AND rr.run_id=ar.id AND rr.approvable=TRUE AND rr.risk_level IN ('low','medium') AND rr.created_at=a.review_created_at
 JOIN approvals ap ON ap.id=a.approval_id AND ap.task_id=t.id AND ap.agent_run_id=ar.id AND ap.approval_type='pr_create'
  AND ap.response='approved' AND ap.responded_by=a.approved_by AND ap.responded_at=a.approval_responded_at
 JOIN users u ON u.id=a.approved_by AND u.organization_id=o.id AND u.deleted_at IS NULL
 WHERE a.task_id=$1 AND a.run_id=$2 AND a.repository_id=$3 AND a.project_id=$4
  AND a.branch=$5 AND a.base_branch=$6 AND a.authorization_generation=$7 AND a.instance_url=$8
  AND t.deleted_at IS NULL AND t.status='reviewing' AND t.target_branch=a.base_branch
  AND w.branch=a.branch AND w.base_branch=a.base_branch AND r.default_branch<>a.branch
  AND ar.completed_at IS NOT NULL AND ar.completed_at<=rr.created_at AND rr.created_at<=ap.responded_at
  AND (ap.expires_at IS NULL OR ap.responded_at<=ap.expires_at)
  AND rc.confirmed_at>=a.attested_at
  AND NOT EXISTS (SELECT 1 FROM pull_requests pr WHERE pr.task_id=t.id)
  AND NOT EXISTS (SELECT 1 FROM agent_runs newer WHERE newer.task_id=t.id AND newer.id<>ar.id
    AND (newer.created_at>ar.created_at OR newer.completed_at>ar.completed_at))
 `

func (f *Factory) reserveGiteaPR(ctx context.Context, task *models.Task, run *models.AgentRun, target repositoryTarget, head, base string) (giteaPRRevision, error) {
	fail := func() (giteaPRRevision, error) {
		return giteaPRRevision{}, fmt.Errorf("%w: Gitea PR publication evidence unavailable or stale", ErrReviewedPush)
	}
	provider, ok := f.forge.(*gateway.GiteaForge)
	if !ok || !strings.HasPrefix(head, "agent/") || head == base || head == "" || base == "" {
		return fail()
	}
	generation, err := ConfiguredGiteaBindingGeneration()
	if err != nil {
		return fail()
	}
	args := []any{task.ID, run.ID, task.RepositoryID, task.ProjectID, head, base, generation, provider.Authority().BaseURL}
	var revision giteaPRRevision
	if err := f.db.QueryRowContext(ctx, "SELECT a.approval_id,a.head_sha,a.base_sha,a.upstream_repository_id"+giteaPRRevisionFrom, args...).Scan(&revision.approvalID, &revision.headSHA, &revision.baseSHA, &revision.upstreamRepositoryID); err != nil {
		return fail()
	}
	if revision.upstreamRepositoryID <= 0 || !fullSHA(revision.headSHA) || !fullSHA(revision.baseSHA) || revision.headSHA == revision.baseSHA {
		return fail()
	}
	for _, expected := range []gateway.ForgeBranchRef{{Ref: head, SHA: revision.headSHA}, {Ref: base, SHA: revision.baseSHA}} {
		actual, err := provider.GetBranch(ctx, f.forgeCredential, target.Owner, target.Name, expected.Ref)
		if err != nil || actual != expected {
			return fail()
		}
	}
	if err := f.verifyGiteaTarget(ctx, task.RepositoryID, task.ProjectID, target); err != nil {
		return fail()
	}
	// Consume exactly this evidence before the non-transactional remote call.
	// Do not retry an ambiguous outcome or create a second PR on another approval.
	args = append(args, revision.approvalID, revision.headSHA, revision.baseSHA, target.Owner, target.Name)
	result, err := f.db.ExecContext(ctx, "INSERT INTO gitea_pr_creation_intents (approval_id,task_id) SELECT a.approval_id,a.task_id"+giteaPRRevisionFrom+
		" AND a.approval_id=$9 AND a.head_sha=$10 AND a.base_sha=$11 AND r.owner=$12 AND r.name=$13 ON CONFLICT DO NOTHING", args...)
	if err != nil {
		return fail()
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fail()
	}
	return revision, nil
}

func (f *Factory) confirmGiteaPR(ctx context.Context, target repositoryTarget, created *gateway.ForgePullRequest, revision giteaPRRevision, head, base string) error {
	fail := fmt.Errorf("%w: Gitea PR outcome not confirmed; reconcile existing intent", ErrReviewedPush)
	provider, ok := f.forge.(*gateway.GiteaForge)
	matches := func(pr *gateway.ForgePullRequest) bool {
		return pr != nil && pr.ID > 0 && pr.Number > 0 && pr.State == "open" && !pr.Draft &&
			pr.Head == (gateway.ForgeBranchRef{Ref: head, SHA: revision.headSHA}) &&
			pr.Base == (gateway.ForgeBranchRef{Ref: base, SHA: revision.baseSHA}) &&
			pr.HTMLURL == fmt.Sprintf("%s/%s/%s/pulls/%d", provider.Authority().BaseURL, target.Owner, target.Name, pr.Number)
	}
	if !ok || !matches(created) {
		return fail
	}
	observed, err := provider.GetMergeSnapshot(ctx, f.forgeCredential, target.Owner, target.Name, created.Number)
	if err != nil || observed.ID != created.ID || observed.State != "open" || observed.Draft || observed.Merged ||
		observed.HTMLURL != created.HTMLURL || observed.Head.Ref != head || observed.Head.SHA != revision.headSHA ||
		observed.Base.Ref != base || observed.Base.SHA != revision.baseSHA ||
		observed.Head.Repo.ID != revision.upstreamRepositoryID || observed.Base.Repo.ID != revision.upstreamRepositoryID {
		return fail
	}
	return nil
}
