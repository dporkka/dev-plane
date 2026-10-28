package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

type evidenceSQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// MergeEvidenceGates atomically adds non-conflicting gate evidence for one
// work item/head. This does NOT replace PutEvidenceBundle's existing semantics.
// Existing gate names are immutable: retries with identical payloads are
// idempotent, while conflicting attempts fail closed for explicit adjudication.
func (db *DB) MergeEvidenceGates(ctx context.Context, incoming repoprotocol.EvidenceBundle) error {
	if strings.TrimSpace(incoming.WorkItemID) == "" || strings.TrimSpace(incoming.HeadSHA) == "" ||
		strings.TrimSpace(incoming.BaseSHA) == "" || len(incoming.Gates) == 0 {
		return errors.New("evidence merge requires work item, head, base and gates")
	}
	seen := make(map[string]bool, len(incoming.Gates))
	for _, g := range incoming.Gates {
		if strings.TrimSpace(g.Name) == "" || seen[g.Name] {
			return fmt.Errorf("duplicate or empty incoming evidence gate %q", g.Name)
		}
		seen[g.Name] = true
	}
	if db.Driver == "sqlite" {
		// BeginTx defaults to a deferred SQLite transaction. BEGIN IMMEDIATE
		// reserves the writer before reading, preventing a concurrent stale read.
		conn, err := db.Conn(ctx)
		if err != nil {
			return fmt.Errorf("reserve evidence merge connection: %w", err)
		}
		// Connection close is best-effort cleanup after the commit or rollback path.
		defer func() { _ = conn.Close() }()
		if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return fmt.Errorf("begin serialized SQLite evidence merge: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			}
		}()
		if err = mergeEvidenceLocked(ctx, conn, db.Driver, incoming); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return fmt.Errorf("commit evidence merge: %w", err)
		}
		committed = true
		return nil
	}
	if db.Driver != "postgres" {
		return fmt.Errorf("unsupported evidence storage driver %q", db.Driver)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin evidence merge transaction: %w", err)
	}
	// Rollback after commit is harmless (sql.ErrTxDone); cleanup is best effort.
	defer func() { _ = tx.Rollback() }()
	if err = mergeEvidenceLocked(ctx, tx, db.Driver, incoming); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit evidence merge: %w", err)
	}
	return nil
}

func mergeEvidenceLocked(ctx context.Context, conn evidenceSQL, driver string, incoming repoprotocol.EvidenceBundle) error {
	payload, err := json.Marshal(incoming)
	if err != nil {
		return err
	}
	insert := `INSERT INTO evidence_bundles (work_item_id, head_sha, base_sha, payload, updated_at)
 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP) ON CONFLICT(work_item_id,head_sha) DO NOTHING`
	if driver == "postgres" {
		insert = rebindPostgres(insert)
	}
	if _, err = conn.ExecContext(ctx, insert, incoming.WorkItemID, incoming.HeadSHA, incoming.BaseSHA, payload); err != nil {
		return fmt.Errorf("reserve evidence key: %w", err)
	}
	read := `SELECT base_sha, payload FROM evidence_bundles WHERE work_item_id = ? AND head_sha = ?`
	if driver == "postgres" {
		read = rebindPostgres(read) + " FOR UPDATE"
	}
	var existingBase string
	var oldPayload []byte
	if err = conn.QueryRowContext(ctx, read, incoming.WorkItemID, incoming.HeadSHA).Scan(&existingBase, &oldPayload); err != nil {
		return fmt.Errorf("lock existing evidence: %w", err)
	}
	if existingBase != incoming.BaseSHA {
		return fmt.Errorf("evidence base SHA conflict for %s@%s", incoming.WorkItemID, incoming.HeadSHA)
	}
	var current repoprotocol.EvidenceBundle
	if err = json.Unmarshal(oldPayload, &current); err != nil {
		return fmt.Errorf("decode existing evidence: %w", err)
	}
	if current.WorkItemID != incoming.WorkItemID || current.HeadSHA != incoming.HeadSHA || current.BaseSHA != incoming.BaseSHA {
		return errors.New("stored evidence identity disagrees with indexed identity")
	}
	indexed := make(map[string]repoprotocol.GateEvidence, len(current.Gates))
	for _, g := range current.Gates {
		if strings.TrimSpace(g.Name) == "" || indexed[g.Name].Name != "" {
			return fmt.Errorf("duplicate or invalid stored gate %q", g.Name)
		}
		indexed[g.Name] = g
	}
	changed := false
	for _, g := range incoming.Gates {
		if old, ok := indexed[g.Name]; ok {
			if !reflect.DeepEqual(old, g) {
				return fmt.Errorf("conflicting execution evidence for gate %q", g.Name)
			}
			continue
		}
		current.Gates = append(current.Gates, g)
		indexed[g.Name] = g
		changed = true
	}
	if !changed {
		return nil
	}
	merged, err := json.Marshal(current)
	if err != nil {
		return err
	}
	update := `UPDATE evidence_bundles SET payload = ?, updated_at = CURRENT_TIMESTAMP
 WHERE work_item_id = ? AND head_sha = ?`
	if driver == "postgres" {
		update = rebindPostgres(update)
	}
	if _, err = conn.ExecContext(ctx, update, merged, incoming.WorkItemID, incoming.HeadSHA); err != nil {
		return fmt.Errorf("store merged evidence: %w", err)
	}
	return nil
}
