package prfactory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	rp "github.com/ai-dev-control-plane/repoprotocol"
)

var ErrGiteaCI = errors.New("Gitea trusted CI evidence unavailable or invalid")

// The signed repository claim is authority-qualified, not a mutable owner/name.
func ciRepository(instance string, id int64) string {
	return fmt.Sprintf("gitea:%s#%d", instance, id)
}

func canonicalCIGates(gates []string) (string, error) {
	if len(gates) == 0 || len(gates) > 64 {
		return "", ErrGiteaCI
	}
	names := append([]string(nil), gates...)
	sort.Strings(names)
	for i, n := range names {
		if n == "" || len(n) > 128 || strings.TrimSpace(n) != n || strings.ContainsAny(n, "\r\n\x00") || n == rp.GateHumanApproval || n == rp.GateIndependentReview || i > 0 && n == names[i-1] {
			return "", ErrGiteaCI
		}
	}
	b, err := json.Marshal(names)
	return string(b), err
}

func validateGiteaCI(bundle rp.EvidenceBundle, item, repo, base, head string, pins map[string]rp.RemoteCIPolicy) error {
	if bundle.WorkItemID != item || bundle.BaseSHA != base || bundle.HeadSHA != head || !fullSHA(base) || !fullSHA(head) || base == head ||
		len(pins) == 0 || len(bundle.Gates) != len(pins) {
		return ErrGiteaCI
	}
	for _, gate := range bundle.Gates {
		p, ok := pins[gate.Name]
		if !ok || p.Repository != repo || gate.RemoteCI == nil || gate.Name == rp.GateHumanApproval || gate.Name == rp.GateIndependentReview {
			return ErrGiteaCI
		}
	}
	if err := bundle.ValidateHeadWithRemoteCI(head, pins); err != nil {
		return ErrGiteaCI
	}
	return nil
}

func decodeCIWork(raw []byte, id, repo, base string) (rp.WorkItem, string, error) {
	var item rp.WorkItem
	if len(raw) > 1<<20 || json.Unmarshal(raw, &item) != nil || item.Validate() != nil || item.ID != id || item.Repository != repo || item.BaseSHA != base {
		return item, "", ErrGiteaCI
	}
	gates, err := canonicalCIGates(item.RequiredGates)
	return item, gates, err
}

// BindGiteaCIWorkItem is an explicit trusted operator action, not an agent
// tool. The operator selects an independently reviewed work item and its gate
// set. No merge authority is conferred. A binding cannot be silently replaced.
func BindGiteaCIWorkItem(ctx context.Context, db *sql.DB, req GiteaMergePreflightRequest, workID string, requiredGates []string) error {
	if workID == "" || strings.TrimSpace(workID) != workID || len(workID) > 256 {
		return ErrGiteaCI
	}
	gates, err := canonicalCIGates(requiredGates)
	if err != nil {
		return err
	}
	verified, err := CheckGiteaMergePreflight(ctx, db, req)
	if err != nil {
		return err
	}
	c, err := loadMergeCandidate(ctx, db, req)
	if err != nil || c.GiteaMergePreflight != verified {
		return ErrGiteaCI
	}
	identity := ciRepository(c.instanceURL, c.UpstreamRepositoryID)
	var raw []byte
	if err := db.QueryRowContext(ctx, "SELECT payload FROM work_items WHERE id=$1 AND repository=$2", workID, identity).Scan(&raw); err != nil {
		return ErrGiteaCI
	}
	_, actual, err := decodeCIWork(raw, workID, identity, c.BaseSHA)
	if err != nil || actual != gates {
		return ErrGiteaCI
	}
	// Reuse the complete local merge eligibility predicate in the INSERT.
	// Place parameter references in ordinal order for SQLite and PostgreSQL.
	query := `WITH eligible(pr_id,approval_id,head_sha,base_sha,repository_id,upstream_pr_id,number,owner,name,branch,base_branch,html_url,instance_url) AS (` + mergeCandidateSQL + `)
 INSERT INTO gitea_ci_work_bindings(pull_request_id,work_item_id,repository_identity,head_sha,base_sha,required_gates)
 SELECT $8,w.id,$9,$10,$11,$12 FROM work_items w
 WHERE w.id=$13 AND w.repository=$14 AND w.payload=$15 AND EXISTS (SELECT 1 FROM eligible WHERE repository_id=$16 AND upstream_pr_id=$17 AND instance_url=$18)
 ON CONFLICT DO NOTHING`
	// JSONB equality uses the original payload, rather than a stale decoded copy.
	// SQLite stores JSON as text; the original bytes must compare as text.
	result, err := db.ExecContext(ctx, query, req.PullRequestID, req.RepositoryID, req.ProjectID, req.OrganizationID, req.ExpectedHeadSHA, req.ExpectedBaseSHA, req.ExpectedGeneration,
		req.PullRequestID, identity, c.HeadSHA, c.BaseSHA, gates, workID, identity, string(raw), c.UpstreamRepositoryID, c.UpstreamPullRequestID, c.instanceURL)
	if err != nil {
		return ErrGiteaCI
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrGiteaCI
	}
	return nil
}

// CheckGiteaCIForWorkItem is read-only. pins must come from trusted operator/CI control
// configuration, never work item, evidence payload, webhook or agent metadata.
// The result is a point-in-time diagnostic, not a merge authorization token.
func CheckGiteaCIForWorkItem(ctx context.Context, db *sql.DB, req GiteaMergePreflightRequest, expectedWorkID string, pins map[string]rp.RemoteCIPolicy) error {
	verified, err := CheckGiteaMergePreflight(ctx, db, req)
	if err != nil {
		return err
	}
	c, err := loadMergeCandidate(ctx, db, req)
	if err != nil || c.GiteaMergePreflight != verified {
		return ErrGiteaCI
	}
	identity := ciRepository(c.instanceURL, c.UpstreamRepositoryID)
	var workID, required string
	var workRaw, evidenceRaw []byte
	err = db.QueryRowContext(ctx, `SELECT b.work_item_id,b.required_gates,w.payload,e.payload
 FROM gitea_ci_work_bindings b JOIN work_items w ON w.id=b.work_item_id AND w.repository=b.repository_identity AND w.state='ready_to_land'
 JOIN evidence_bundles e ON e.work_item_id=b.work_item_id AND e.head_sha=b.head_sha AND e.base_sha=b.base_sha
 WHERE b.pull_request_id=$1 AND b.repository_identity=$2 AND b.head_sha=$3 AND b.base_sha=$4`,
		req.PullRequestID, identity, c.HeadSHA, c.BaseSHA).Scan(&workID, &required, &workRaw, &evidenceRaw)
	if err != nil || workID != expectedWorkID || len(evidenceRaw) > 4<<20 {
		return ErrGiteaCI
	}
	item, gates, err := decodeCIWork(workRaw, workID, identity, c.BaseSHA)
	if err != nil || gates != required || item.State != rp.WorkReadyToLand {
		return ErrGiteaCI
	}
	names := make([]string, 0, len(pins))
	for name := range pins {
		names = append(names, name)
	}
	pinned, err := canonicalCIGates(names)
	if err != nil || pinned != required {
		return ErrGiteaCI
	}
	var bundle rp.EvidenceBundle
	if json.Unmarshal(evidenceRaw, &bundle) != nil {
		return ErrGiteaCI
	}
	if err := validateGiteaCI(bundle, workID, identity, c.BaseSHA, c.HeadSHA, pins); err != nil {
		return err
	}
	latest, err := loadMergeCandidate(ctx, db, req)
	if err != nil || latest != c {
		return ErrGiteaCI
	}
	return nil
}
