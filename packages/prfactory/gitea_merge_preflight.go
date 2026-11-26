package prfactory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/gateway"
)

var ErrGiteaMergePreflight = errors.New("Gitea merge revision preflight failed")

// GiteaMergePreflightRequest is a trusted operator input, not a tenant
// authorization mechanism. All identifiers and SHAs must be explicit.
type GiteaMergePreflightRequest struct {
	PullRequestID, RepositoryID, ProjectID, OrganizationID string
	ExpectedHeadSHA, ExpectedBaseSHA                       string
	ExpectedGeneration                                     int64
}

// GiteaMergePreflight describes a point-in-time observation, never permission
// to merge. CI, capability policy, branch protection and fresh authorization
// are still required. Do not persist this value as an approval token.
type GiteaMergePreflight struct {
	PullRequestID, ApprovalID, HeadSHA, BaseSHA string
	UpstreamRepositoryID, UpstreamPullRequestID int64
	Number                                      int
}

type mergeCandidate struct {
	GiteaMergePreflight
	owner, name, branch, baseBranch, htmlURL, instanceURL string
}

const mergeCandidateSQL = `
 SELECT pr.id,a.approval_id,a.head_sha,a.base_sha,b.upstream_repository_id,pr.number,
 r.owner,r.name,a.branch,a.base_branch,pr.url,b.instance_url
 FROM pull_requests pr
 JOIN gitea_pr_creation_intents i ON i.task_id=pr.task_id
 JOIN gitea_revision_attestations a ON a.approval_id=i.approval_id AND a.task_id=pr.task_id AND a.run_id=pr.run_id
  AND a.repository_id=pr.repository_id AND a.branch=pr.branch AND a.base_branch=pr.base_branch
 JOIN gitea_publication_receipts rc ON rc.approval_id=a.approval_id AND rc.observed_head_sha=a.head_sha
 JOIN tasks t ON t.id=a.task_id AND t.repository_id=a.repository_id AND t.project_id=a.project_id
  AND t.workspace_id=a.workspace_id AND t.deleted_at IS NULL AND t.status='pr_created' AND t.target_branch=a.base_branch
 JOIN projects p ON p.id=a.project_id AND p.organization_id=a.organization_id AND p.deleted_at IS NULL
 JOIN organizations o ON o.id=p.organization_id AND o.deleted_at IS NULL
 JOIN repositories r ON r.id=a.repository_id AND r.project_id=p.id AND r.deleted_at IS NULL
 JOIN gitea_repository_bindings b ON b.repository_id=r.id AND b.project_id=p.id AND b.organization_id=o.id
  AND b.full_name=r.full_name AND b.instance_url=a.instance_url AND b.upstream_repository_id=a.upstream_repository_id
  AND b.authorization_generation=a.authorization_generation AND b.revoked_at IS NULL
 JOIN agent_runs ar ON ar.id=a.run_id AND ar.task_id=t.id AND ar.workspace_id=a.workspace_id AND ar.status IN ('completed','reviewed')
 JOIN workspaces w ON w.id=a.workspace_id AND w.task_id=t.id AND w.repository_id=r.id
  AND w.deleted_at IS NULL AND w.status='ready' AND w.runtime_provider='docker'
  AND (w.worktree_path IS NULL OR w.worktree_path='') AND w.branch=a.branch AND w.base_branch=a.base_branch
 JOIN review_reports rr ON rr.id=a.review_id AND rr.run_id=ar.id AND rr.approvable=TRUE
  AND rr.risk_level IN ('low','medium') AND rr.created_at=a.review_created_at
 JOIN approvals ap ON ap.id=a.approval_id AND ap.task_id=t.id AND ap.agent_run_id=ar.id AND ap.approval_type='pr_create'
  AND ap.response='approved' AND ap.responded_by=a.approved_by AND ap.responded_at=a.approval_responded_at
 JOIN users u ON u.id=a.approved_by AND u.organization_id=o.id AND u.deleted_at IS NULL
 WHERE pr.id=$1 AND a.repository_id=$2 AND a.project_id=$3 AND a.organization_id=$4
  AND a.head_sha=$5 AND a.base_sha=$6 AND a.authorization_generation=$7
  AND pr.state='open' AND pr.draft=FALSE AND r.default_branch<>a.branch
  AND ar.completed_at IS NOT NULL AND ar.completed_at<=rr.created_at AND rr.created_at<=ap.responded_at
  AND (ap.expires_at IS NULL OR ap.responded_at<=ap.expires_at)
  AND rc.confirmed_at>=a.attested_at AND i.requested_at>=rc.confirmed_at
  AND NOT EXISTS (SELECT 1 FROM agent_runs newer WHERE newer.task_id=t.id AND newer.id<>ar.id
   AND (newer.created_at>ar.created_at OR newer.completed_at>ar.completed_at))
 `

func loadMergeCandidate(ctx context.Context, db *sql.DB, req GiteaMergePreflightRequest) (mergeCandidate, error) {
	var c mergeCandidate
	err := db.QueryRowContext(ctx, mergeCandidateSQL, req.PullRequestID, req.RepositoryID, req.ProjectID, req.OrganizationID, req.ExpectedHeadSHA, req.ExpectedBaseSHA, req.ExpectedGeneration).
		Scan(&c.PullRequestID, &c.ApprovalID, &c.HeadSHA, &c.BaseSHA, &c.UpstreamRepositoryID, &c.Number, &c.owner, &c.name, &c.branch, &c.baseBranch, &c.htmlURL, &c.instanceURL)
	if err != nil || !strings.HasPrefix(c.branch, "agent/") || c.branch == c.baseBranch || c.Number <= 0 || c.UpstreamRepositoryID <= 0 {
		return mergeCandidate{}, ErrGiteaMergePreflight
	}
	return c, nil
}

// CheckGiteaMergePreflight performs only SELECTs and GETs. It does not merge,
// issue an approval, or alter local state. The remote base is checked directly
// because a PR's base SHA need not represent the current target branch tip.
func CheckGiteaMergePreflight(ctx context.Context, db *sql.DB, req GiteaMergePreflightRequest) (GiteaMergePreflight, error) {
	fail := func(reason string) (GiteaMergePreflight, error) {
		return GiteaMergePreflight{}, fmt.Errorf("%w: %s", ErrGiteaMergePreflight, reason)
	}
	if db == nil || req.PullRequestID == "" || req.RepositoryID == "" || req.ProjectID == "" || req.OrganizationID == "" ||
		!fullSHA(req.ExpectedHeadSHA) || !fullSHA(req.ExpectedBaseSHA) || req.ExpectedHeadSHA == req.ExpectedBaseSHA || req.ExpectedGeneration <= 0 {
		return fail("explicit identity and distinct full revisions required")
	}
	generation, err := ConfiguredGiteaBindingGeneration()
	if err != nil || generation != req.ExpectedGeneration {
		return fail("binding generation mismatch")
	}
	forge, cred, err := ConfiguredGiteaBindingForge()
	if err != nil {
		return fail("forge unavailable")
	}
	c, err := loadMergeCandidate(ctx, db, req)
	if err != nil || c.instanceURL != forge.Authority().BaseURL {
		return fail("current local revision evidence unavailable")
	}
	binding, err := VerifyGiteaRepositoryBinding(ctx, db, forge, cred, req.RepositoryID, req.ProjectID)
	if err != nil || binding.OrganizationID != req.OrganizationID || binding.UpstreamRepositoryID != c.UpstreamRepositoryID ||
		binding.Owner != c.owner || binding.Name != c.name || binding.AuthorizationGeneration != generation {
		return fail("repository authority changed")
	}
	expectedURL := fmt.Sprintf("%s/%s/%s/pulls/%d", c.instanceURL, c.owner, c.name, c.Number)
	if c.htmlURL != expectedURL {
		return fail("local PR URL mismatch")
	}
	for _, expected := range []gateway.ForgeBranchRef{{Ref: c.branch, SHA: c.HeadSHA}, {Ref: c.baseBranch, SHA: c.BaseSHA}} {
		actual, err := forge.GetBranch(ctx, cred, c.owner, c.name, expected.Ref)
		if err != nil || actual != expected {
			return fail("remote branch revision changed")
		}
	}
	remote, err := forge.GetMergeSnapshot(ctx, cred, c.owner, c.name, c.Number)
	if err != nil || remote.State != "open" || remote.Draft || remote.Merged || remote.HTMLURL != expectedURL ||
		remote.Head.Repo.ID != c.UpstreamRepositoryID || remote.Base.Repo.ID != c.UpstreamRepositoryID ||
		remote.Head.Ref != c.branch || remote.Base.Ref != c.baseBranch || remote.Head.SHA != c.HeadSHA || remote.Base.SHA != c.BaseSHA {
		return fail("remote PR revision or repository identity changed")
	}
	latest, err := loadMergeCandidate(ctx, db, req)
	if err != nil || latest != c {
		return fail("local authority changed during remote checks")
	}
	c.UpstreamPullRequestID = remote.ID
	return c.GiteaMergePreflight, nil
}
