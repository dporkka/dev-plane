package repogate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (g *Gate) loadRecord(ctx context.Context, runID string) (Record, error) {
	var record Record
	var changeID, integrationBranch, integratedCommit, initialJSON, finalJSON sql.NullString
	err := g.db.QueryRowContext(ctx, `
		SELECT run_id, task_id, workspace_id, commit_id, change_id, source_branch,
		       target_branch, integration_branch, integrated_commit, status,
		       initial_verification, final_verification
		FROM verified_candidates
		WHERE run_id = $1
	`, runID).Scan(
		&record.RunID, &record.TaskID, &record.WorkspaceID, &record.CommitID, &changeID,
		&record.SourceBranch, &record.TargetBranch, &integrationBranch, &integratedCommit,
		&record.Status, &initialJSON, &finalJSON,
	)
	if err != nil {
		return Record{}, err
	}
	record.ChangeID = changeID.String
	record.IntegrationBranch = integrationBranch.String
	record.IntegratedCommit = integratedCommit.String
	record.InitialEvidence = decodeEvidence(initialJSON.String)
	record.FinalEvidence = decodeEvidence(finalJSON.String)
	return record, nil
}

func (g *Gate) saveRecord(ctx context.Context, record Record) error {
	initialJSON, err := json.Marshal(record.InitialEvidence)
	if err != nil {
		return fmt.Errorf("marshal initial verification evidence: %w", err)
	}
	finalJSON, err := json.Marshal(record.FinalEvidence)
	if err != nil {
		return fmt.Errorf("marshal final verification evidence: %w", err)
	}
	now := time.Now().UTC()
	_, err = g.db.ExecContext(ctx, `
		INSERT INTO verified_candidates (
			run_id, task_id, workspace_id, commit_id, change_id, source_branch,
			target_branch, integration_branch, integrated_commit, status,
			initial_verification, final_verification, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
		ON CONFLICT (run_id) DO UPDATE SET
			commit_id = EXCLUDED.commit_id,
			change_id = EXCLUDED.change_id,
			source_branch = EXCLUDED.source_branch,
			target_branch = EXCLUDED.target_branch,
			integration_branch = EXCLUDED.integration_branch,
			integrated_commit = EXCLUDED.integrated_commit,
			status = EXCLUDED.status,
			initial_verification = EXCLUDED.initial_verification,
			final_verification = EXCLUDED.final_verification,
			updated_at = EXCLUDED.updated_at
	`, record.RunID, record.TaskID, record.WorkspaceID, record.CommitID, nullable(record.ChangeID),
		record.SourceBranch, record.TargetBranch, nullable(record.IntegrationBranch), nullable(record.IntegratedCommit),
		record.Status, string(initialJSON), string(finalJSON), now)
	if err != nil {
		return fmt.Errorf("persist verified candidate: %w", err)
	}
	return nil
}

func decodeEvidence(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var evidence map[string]string
	if json.Unmarshal([]byte(raw), &evidence) != nil {
		return nil
	}
	return evidence
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
