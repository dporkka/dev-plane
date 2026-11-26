package prfactory

import (
	"context"
	"fmt"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

// saveGiteaPRIdentity commits the local PR, independently confirmed identity,
// and task transition together. The earlier creation intent is deliberately
// outside this transaction, so a crash or failed commit cannot enable a retry.
func (f *Factory) saveGiteaPRIdentity(ctx context.Context, task *models.Task, run *models.AgentRun, target repositoryTarget, pr *models.PullRequest, upstreamID int64, revision giteaPRRevision) error {
	fail := fmt.Errorf("%w: upstream PR exists but local identity not committed; reconcile intent", ErrReviewedPush)
	provider, ok := f.forge.(*gateway.GiteaForge)
	generation, err := ConfiguredGiteaBindingGeneration()
	if !ok || err != nil || upstreamID <= 0 || pr.Validate() != nil {
		return fail
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return fail
	}
	defer func() { _ = tx.Rollback() }()
	args := []any{task.ID, run.ID, task.RepositoryID, task.ProjectID, pr.Branch, pr.BaseBranch, generation, provider.Authority().BaseURL,
		revision.approvalID, revision.headSHA, revision.baseSHA, target.Owner, target.Name, revision.upstreamRepositoryID,
		pr.ID, pr.Number, pr.Title, pr.Body, pr.URL, pr.CreatedBy, pr.CreatedAt}
	// Revalidate the exact original evidence inside the insert rather than rely
	// on an earlier read. Only the task's consumed creation intent is eligible.
	query := `WITH eligible AS (
 SELECT a.task_id,a.run_id,a.repository_id,a.branch,a.base_branch` +
		giteaPRRevisionFrom + ` AND a.approval_id=$9 AND a.head_sha=$10 AND a.base_sha=$11 AND r.owner=$12 AND r.name=$13
 AND a.upstream_repository_id=$14
 AND EXISTS (SELECT 1 FROM gitea_pr_creation_intents i WHERE i.approval_id=a.approval_id AND i.task_id=t.id)
 )
 INSERT INTO pull_requests
 (id,task_id,run_id,repository_id,number,title,body,branch,base_branch,url,state,draft,created_by,created_at,updated_at)
 SELECT $15,e.task_id,e.run_id,e.repository_id,$16,$17,$18,e.branch,e.base_branch,$19,'open',FALSE,$20,$21,$21 FROM eligible e`
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fail
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return fail
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO gitea_pr_identity_receipts
 (pull_request_id,approval_id,instance_url,upstream_repository_id,upstream_pr_id,upstream_pr_number,head_sha,base_sha)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, pr.ID, revision.approvalID, provider.Authority().BaseURL, revision.upstreamRepositoryID, upstreamID, pr.Number, revision.headSHA, revision.baseSHA)
	if err != nil {
		return fail
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return fail
	}
	result, err = tx.ExecContext(ctx, `UPDATE tasks SET status = 'pr_created',updated_at=$1 WHERE id=$2 AND status='reviewing' AND deleted_at IS NULL`, pr.UpdatedAt, task.ID)
	if err != nil {
		return fail
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return fail
	}
	if err := tx.Commit(); err != nil {
		return fail
	}
	return nil
}
