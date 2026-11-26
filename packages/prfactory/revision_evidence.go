package prfactory

import (
	"context"
	"database/sql"
	"fmt"
)

// The ledger is append-only through this broker. Database administrators still
// control the database; production roles must not have UPDATE/DELETE access.
// An intent is never deleted or reused, even when the remote outcome is unknown.
func recordReviewedPushIntent(ctx context.Context, db *sql.DB, req ReviewedPushRequest, instanceURL, cloneURL string) error {
	// Match durable authority again in the same statement that consumes the
	// approval. SELECT parameters appear in ordinal order for SQLite and PG.
	result, err := db.ExecContext(ctx, `
 INSERT INTO gitea_revision_attestations
 (approval_id,task_id,run_id,review_id,workspace_id,repository_id,project_id,organization_id,
 approved_by,instance_url,upstream_repository_id,authorization_generation,branch,base_branch,
 base_sha,head_sha,approval_responded_at,review_created_at)
 SELECT a.id,t.id,ar.id,rr.id,w.id,r.id,p.id,o.id,u.id,b.instance_url,b.upstream_repository_id,
 b.authorization_generation,w.branch,w.base_branch,$1,$2,a.responded_at,rr.created_at
 FROM tasks t
 JOIN projects p ON p.id=t.project_id AND p.deleted_at IS NULL
 JOIN organizations o ON o.id=p.organization_id AND o.deleted_at IS NULL
 JOIN repositories r ON r.id=t.repository_id AND r.project_id=t.project_id AND r.deleted_at IS NULL
 JOIN gitea_repository_bindings b ON b.repository_id=r.id AND b.project_id=p.id AND b.organization_id=o.id
   AND b.revoked_at IS NULL AND b.full_name=r.full_name
   AND b.instance_url=$3 AND b.upstream_repository_id=$4 AND b.authorization_generation=$5
 JOIN agent_runs ar ON ar.task_id=t.id AND ar.id=$6 AND ar.status IN ('completed','reviewed')
 JOIN workspaces w ON w.id=ar.workspace_id AND w.id=t.workspace_id AND w.task_id=t.id
   AND w.repository_id=r.id AND w.deleted_at IS NULL AND w.status='ready' AND w.runtime_provider='docker'
 JOIN review_reports rr ON rr.run_id=ar.id AND rr.id=$7 AND rr.approvable=TRUE
 JOIN approvals a ON a.task_id=t.id AND a.agent_run_id=ar.id AND a.id=$8
   AND a.approval_type='pr_create' AND a.response='approved' AND a.responded_at IS NOT NULL
 JOIN users u ON u.id=a.responded_by AND u.organization_id=o.id AND u.deleted_at IS NULL
 WHERE t.id=$9 AND r.id=$10 AND p.id=$11 AND o.id=$12
   AND t.deleted_at IS NULL AND t.status='reviewing' AND r.clone_url=$13
   AND w.branch=$14 AND w.base_branch=$15 AND t.target_branch=w.base_branch AND r.default_branch<>w.branch
   AND ar.completed_at IS NOT NULL AND ar.completed_at<=rr.created_at AND rr.created_at<=a.responded_at
   AND (a.expires_at IS NULL OR a.responded_at<=a.expires_at)
 ON CONFLICT DO NOTHING
 `, req.BaseSHA, req.HeadSHA, instanceURL, req.UpstreamRepositoryID, req.ExpectedGeneration,
		req.RunID, req.ReviewID, req.ApprovalID, req.TaskID, req.RepositoryID, req.ProjectID,
		req.OrganizationID, cloneURL, req.Branch, req.BaseBranch)
	if err != nil {
		return fmt.Errorf("%w: cannot record revision intent", ErrReviewedPush)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("%w: revision intent not created; reconcile existing evidence", ErrReviewedPush)
	}
	return nil
}

// A receipt is evidence of the independently observed remote SHA, not proof
// that approval remains valid forever. Future PR/merge gates must revalidate.
func recordReviewedPushReceipt(ctx context.Context, db *sql.DB, req ReviewedPushRequest) error {
	result, err := db.ExecContext(ctx, `
 INSERT INTO gitea_publication_receipts (approval_id,observed_head_sha)
 SELECT approval_id,head_sha FROM gitea_revision_attestations
 WHERE approval_id=$1 AND head_sha=$2
 `, req.ApprovalID, req.HeadSHA)
	if err != nil {
		return fmt.Errorf("%w: remote head confirmed but receipt not recorded; reconcile, do not retry", ErrReviewedPush)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("%w: receipt not confirmed; reconcile, do not retry", ErrReviewedPush)
	}
	return nil
}
