package egresspolicy

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func receiptTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE agent_egress_receipts (
   tenant_id TEXT NOT NULL,
   agent_run_id TEXT NOT NULL,
   runtime_session_id TEXT NOT NULL,
   receipt_id TEXT NOT NULL,
   created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
   PRIMARY KEY(agent_run_id,receipt_id)
 )`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSQLReceiptStoreRejectsReplayAcrossInstances(t *testing.T) {
	db := receiptTestDB(t)
	ctx := context.Background()
	identity := SessionIdentity{TenantID: "tenant", AgentID: "run", RuntimeSessionID: "session"}
	first := NewSQLReceiptStore(db)
	second := NewSQLReceiptStore(db)
	if err := first.Reserve(ctx, identity, "receipt-1"); err != nil {
		t.Fatal(err)
	}
	if err := second.Reserve(ctx, identity, "receipt-1"); !errors.Is(err, ErrDenied) {
		t.Fatalf("replay accepted: %v", err)
	}
	if err := second.Reserve(ctx, identity, "receipt-2"); err != nil {
		t.Fatalf("distinct receipt denied: %v", err)
	}
	identity.AgentID = "different-run"
	if err := second.Reserve(ctx, identity, "receipt-1"); err != nil {
		t.Fatalf("different run was blocked: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM agent_egress_receipts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("wanted 3 durable records, got %d", n)
	}
}

func TestSQLReceiptStoreConcurrentDuplicateHasSingleWinner(t *testing.T) {
	db := receiptTestDB(t)
	db.SetMaxOpenConns(1)
	identity := SessionIdentity{TenantID: "tenant", AgentID: "run", RuntimeSessionID: "session"}
	const attempts = 12
	var wg sync.WaitGroup
	successes := make(chan bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := NewSQLReceiptStore(db).Reserve(context.Background(), identity, "same-receipt")
			if err != nil && !errors.Is(err, ErrDenied) {
				t.Errorf("unexpected persistence failure: %v", err)
			}
			successes <- err == nil
		}()
	}
	wg.Wait()
	close(successes)
	var winners int
	for v := range successes {
		if v {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected one reservation winner, got %d", winners)
	}
}

func TestSQLReceiptStoreFailsClosedOnMissingDB(t *testing.T) {
	identity := SessionIdentity{TenantID: "tenant", AgentID: "run", RuntimeSessionID: "session"}
	if err := NewSQLReceiptStore(nil).Reserve(context.Background(), identity, "id"); !errors.Is(err, ErrDenied) {
		t.Fatalf("nil DB allowed: %v", err)
	}
	if err := NewSQLReceiptStore(receiptTestDB(t)).Reserve(context.Background(), identity, "../../escape"); !errors.Is(err, ErrDenied) {
		t.Fatalf("invalid receipt accepted: %v", err)
	}
	if err := NewSQLReceiptStore(receiptTestDB(t)).Reserve(context.Background(), SessionIdentity{}, "valid-id"); !errors.Is(err, ErrDenied) {
		t.Fatalf("empty identity accepted: %v", err)
	}
}
