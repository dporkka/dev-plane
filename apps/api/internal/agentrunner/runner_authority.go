package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/execution"
)

type approvalAuthorityMetadata struct {
	AuthorityGrant *struct {
		Operation string `json:"operation"`
		Resource  string `json:"resource"`
		Revision  string `json:"revision"`
	} `json:"authority_grant"`
	AuthorityDigest     string `json:"authority_digest"`
	AuthorityConsumedAt string `json:"authority_consumed_at,omitempty"`
}

// consumeApprovedAuthority atomically consumes one exact approved capability
// grant for a run. Approval is single-use: once the metadata compare-and-swap
// succeeds, subsequent attempts must obtain a new approval.
//
// Legacy capability approvals without an authority grant/digest are deliberately
// ignored. They are evidence that a human approved something, but not evidence
// of the exact operation/resource/revision that may execute.
func (r *Runner) consumeApprovedAuthority(ctx context.Context, runID string, requested execution.Grant) (bool, error) {
	if r == nil || r.db == nil || strings.TrimSpace(runID) == "" {
		return false, nil
	}
	if err := requested.Validate(); err != nil {
		return false, fmt.Errorf("validate requested authority: %w", err)
	}

	now := time.Now().UTC()
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, metadata
		FROM approvals
		WHERE agent_run_id = $1
		  AND approval_type = $2
		  AND response = 'approved'
		  AND (expires_at IS NULL OR expires_at > $3)
		ORDER BY responded_at DESC, created_at DESC
	`, runID, "capability:"+requested.Operation, now)
	if err != nil {
		return false, fmt.Errorf("load approved authority: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var approvalID string
		var raw []byte
		if err := rows.Scan(&approvalID, &raw); err != nil {
			return false, fmt.Errorf("scan approved authority: %w", err)
		}

		var parsed approvalAuthorityMetadata
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return false, fmt.Errorf("decode authority approval %s: %w", approvalID, err)
		}
		if parsed.AuthorityGrant == nil || strings.TrimSpace(parsed.AuthorityDigest) == "" {
			// Pre-authority approvals are intentionally not treated as grants.
			continue
		}
		if parsed.AuthorityConsumedAt != "" {
			continue
		}

		approved := execution.Grant{
			Operation: parsed.AuthorityGrant.Operation,
			Resource:  parsed.AuthorityGrant.Resource,
			Revision:  parsed.AuthorityGrant.Revision,
		}
		manifest, err := execution.NewManifest(approved)
		if err != nil {
			return false, fmt.Errorf("validate authority approval %s: %w", approvalID, err)
		}
		digest, err := manifest.Digest()
		if err != nil {
			return false, fmt.Errorf("digest authority approval %s: %w", approvalID, err)
		}
		if digest != parsed.AuthorityDigest {
			return false, fmt.Errorf("authority approval %s digest mismatch", approvalID)
		}
		if approved != requested {
			continue
		}

		var metadata map[string]json.RawMessage
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return false, fmt.Errorf("decode authority approval %s for consumption: %w", approvalID, err)
		}
		consumedAt, err := json.Marshal(now.Format(time.RFC3339Nano))
		if err != nil {
			return false, fmt.Errorf("encode authority consumption timestamp: %w", err)
		}
		metadata["authority_consumed_at"] = consumedAt
		updated, err := json.Marshal(metadata)
		if err != nil {
			return false, fmt.Errorf("encode consumed authority approval %s: %w", approvalID, err)
		}

		result, err := r.db.ExecContext(ctx, `
			UPDATE approvals
			SET metadata = $1, updated_at = $2
			WHERE id = $3
			  AND metadata = $4
			  AND response = 'approved'
		`, string(updated), now, approvalID, string(raw))
		if err != nil {
			return false, fmt.Errorf("consume authority approval %s: %w", approvalID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("check authority approval %s consumption: %w", approvalID, err)
		}
		if affected == 1 {
			return true, nil
		}
	}

	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate approved authority: %w", err)
	}
	return false, nil
}
