package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

// Opt-in integration coverage against a disposable, isolated PostgreSQL
// database. Never point this at an existing application database.
// DEVPLANE_TEST_POSTGRES_URL must be a dedicated test database URL.
func TestPostgresEvidenceMergeRealConcurrentTransactions(t *testing.T) {
	dsn := os.Getenv("DEVPLANE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set DEVPLANE_TEST_POSTGRES_URL to a disposable PostgreSQL test database")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Path != "/devplane_ciqual" {
		t.Fatal("refusing to modify PostgreSQL database except the dedicated devplane_ciqual database")
	}
	caseID := fmt.Sprintf("pg-qual-%d", time.Now().UnixNano())
	first, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = first.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS evidence_bundles (
   work_item_id TEXT NOT NULL, head_sha TEXT NOT NULL,base_sha TEXT NOT NULL,
   payload JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
   updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
   PRIMARY KEY (work_item_id,head_sha))`)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("simultaneous independent writers preserve all gates", func(t *testing.T) {
		const count = 36
		var wg sync.WaitGroup
		errs := make(chan error, count)
		for i := 0; i < count; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				db := first
				if n%2 == 1 {
					db = second
				}
				incoming := repoprotocol.EvidenceBundle{
					WorkItemID: caseID + "-many", HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40),
					Gates: []repoprotocol.GateEvidence{{Name: fmt.Sprintf("gate-%02d", n), Status: repoprotocol.GatePending}},
				}
				errs <- db.MergeEvidenceGates(ctx, incoming)
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent PostgreSQL merge failed: %v", err)
			}
		}
		got, err := first.GetEvidenceBundle(ctx, caseID+"-many", strings.Repeat("a", 40))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Gates) != count {
			t.Fatalf("lost PostgreSQL gate evidence: got %d want %d", len(got.Gates), count)
		}
	})

	t.Run("conflicts roll back and head isolation stays intact", func(t *testing.T) {
		head := strings.Repeat("a", 40)
		base := strings.Repeat("b", 40)
		makeBundle := func(head, base, name string, status repoprotocol.GateStatus) repoprotocol.EvidenceBundle {
			return repoprotocol.EvidenceBundle{WorkItemID: caseID + "-check", HeadSHA: head, BaseSHA: base,
				Gates: []repoprotocol.GateEvidence{{Name: name, Status: status}}}
		}
		original := makeBundle(head, base, "test", repoprotocol.GatePassed)
		if err := first.MergeEvidenceGates(ctx, original); err != nil {
			t.Fatal(err)
		}
		if err := second.MergeEvidenceGates(ctx, original); err != nil {
			t.Fatalf("idempotent duplicate rejected: %v", err)
		}
		if err := second.MergeEvidenceGates(ctx, makeBundle(head, base, "test", repoprotocol.GateFailed)); err == nil {
			t.Fatal("accepted conflicting gate status")
		}
		if err := second.MergeEvidenceGates(ctx, makeBundle(head, strings.Repeat("c", 40), "new", repoprotocol.GatePassed)); err == nil {
			t.Fatal("accepted base mismatch")
		}
		if err := first.MergeEvidenceGates(ctx, makeBundle(head, base, "new", repoprotocol.GatePending)); err != nil {
			t.Fatalf("connection blocked after transaction rollback: %v", err)
		}
		got, err := first.GetEvidenceBundle(ctx, caseID+"-check", head)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Gates) != 2 || got.Gates[0].Status != repoprotocol.GatePassed {
			t.Fatalf("gate conflict overwrote record: %+v", got)
		}
		other := makeBundle(strings.Repeat("d", 40), base, "test", repoprotocol.GatePassed)
		if err := second.MergeEvidenceGates(ctx, other); err != nil {
			t.Fatalf("head isolation failure: %v", err)
		}
		check, err := second.GetEvidenceBundle(ctx, caseID+"-check", other.HeadSHA)
		if err != nil || len(check.Gates) != 1 {
			t.Fatalf("separate head missing: %+v %v", check, err)
		}
	})
}
