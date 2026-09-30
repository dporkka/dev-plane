package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func (db *DB) PutWorkItem(ctx context.Context, item repoprotocol.WorkItem) error {
	if err := item.Validate(); err != nil {
		return fmt.Errorf("validate work item: %w", err)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal work item: %w", err)
	}

	query := `
		INSERT INTO work_items (id, repository, state, claimed_by, lease_until, payload, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			repository = excluded.repository,
			state = excluded.state,
			claimed_by = excluded.claimed_by,
			lease_until = excluded.lease_until,
			payload = excluded.payload,
			updated_at = CURRENT_TIMESTAMP
	`
	args := []any{item.ID, item.Repository, string(item.State), nullableString(item.ClaimedBy), item.LeaseUntil, payload}
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("put work item %s: %w", item.ID, err)
	}
	return nil
}

func (db *DB) GetWorkItem(ctx context.Context, id string) (repoprotocol.WorkItem, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return repoprotocol.WorkItem{}, errors.New("work item id is required")
	}
	query := "SELECT payload FROM work_items WHERE id = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return repoprotocol.WorkItem{}, fmt.Errorf("get work item %s: %w", id, err)
	}
	var item repoprotocol.WorkItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return repoprotocol.WorkItem{}, fmt.Errorf("decode work item %s: %w", id, err)
	}
	return item, nil
}

func (db *DB) PutEvidenceBundle(ctx context.Context, bundle repoprotocol.EvidenceBundle) error {
	if strings.TrimSpace(bundle.WorkItemID) == "" {
		return errors.New("evidence work_item_id is required")
	}
	if strings.TrimSpace(bundle.BaseSHA) == "" {
		return errors.New("evidence base_sha is required")
	}
	if strings.TrimSpace(bundle.HeadSHA) == "" {
		return errors.New("evidence head_sha is required")
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshal evidence bundle: %w", err)
	}

	query := `
		INSERT INTO evidence_bundles (work_item_id, head_sha, base_sha, payload, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(work_item_id, head_sha) DO UPDATE SET
			base_sha = excluded.base_sha,
			payload = excluded.payload,
			updated_at = CURRENT_TIMESTAMP
	`
	args := []any{bundle.WorkItemID, bundle.HeadSHA, bundle.BaseSHA, payload}
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("put evidence bundle %s@%s: %w", bundle.WorkItemID, bundle.HeadSHA, err)
	}
	return nil
}

func (db *DB) GetEvidenceBundle(ctx context.Context, workItemID, headSHA string) (repoprotocol.EvidenceBundle, error) {
	workItemID = strings.TrimSpace(workItemID)
	headSHA = strings.TrimSpace(headSHA)
	if workItemID == "" || headSHA == "" {
		return repoprotocol.EvidenceBundle{}, errors.New("evidence work_item_id and head_sha are required")
	}
	query := "SELECT payload FROM evidence_bundles WHERE work_item_id = ? AND head_sha = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, query, workItemID, headSHA).Scan(&raw); err != nil {
		return repoprotocol.EvidenceBundle{}, fmt.Errorf("get evidence bundle %s@%s: %w", workItemID, headSHA, err)
	}
	var bundle repoprotocol.EvidenceBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return repoprotocol.EvidenceBundle{}, fmt.Errorf("decode evidence bundle %s@%s: %w", workItemID, headSHA, err)
	}
	return bundle, nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func rebindPostgres(query string) string {
	var b strings.Builder
	arg := 1
	for _, r := range query {
		if r == '?' {
			fmt.Fprintf(&b, "$%d", arg)
			arg++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
