package egresspolicy

import (
	"context"
	"database/sql"
	"strings"
)

// ReceiptStore reserves a unique delivery identity *before* any outbound
// network request. A durable implementation prevents duplicate requests
// across independent worker processes.
type ReceiptStore interface {
	Reserve(ctx context.Context, scope SessionIdentity, receiptID string) error
}

// SQLReceiptStore enforces durable at-most-once receipt reservations via the
// UNIQUE(agent_run_id, receipt_id) constraint installed in migration 025.
// Failed requests retain reservations: retries must use a new receipt ID.
type SQLReceiptStore struct{ db *sql.DB }

func NewSQLReceiptStore(db *sql.DB) *SQLReceiptStore {
	return &SQLReceiptStore{db: db}
}

func (s *SQLReceiptStore) Reserve(ctx context.Context, scope SessionIdentity, receiptID string) error {
	if s == nil || s.db == nil || ctx == nil || ctx.Err() != nil ||
		scope.TenantID == "" || strings.TrimSpace(scope.TenantID) != scope.TenantID ||
		scope.AgentID == "" || strings.TrimSpace(scope.AgentID) != scope.AgentID ||
		scope.RuntimeSessionID == "" || strings.TrimSpace(scope.RuntimeSessionID) != scope.RuntimeSessionID ||
		!validReceiptID(receiptID) {
		return ErrDenied
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO agent_egress_receipts
    (tenant_id, agent_run_id, runtime_session_id, receipt_id)
VALUES ($1,$2,$3,$4)
ON CONFLICT (agent_run_id, receipt_id) DO NOTHING
`, scope.TenantID, scope.AgentID, scope.RuntimeSessionID, receiptID)
	if err != nil {
		return ErrDenied
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrDenied
	}
	return nil
}
