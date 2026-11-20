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

// validateEvidenceMergeTestDSN rejects unsafe URL overrides *before*
// opening any database connections. lib/pq allows query parameters to
// override the database named in the URL path.
func validateEvidenceMergeTestDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" ||
		(parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Path != "/devplane_ciqual" || parsed.Fragment != "" {
		return fmt.Errorf("refusing PostgreSQL integration test outside dedicated devplane_ciqual database")
	}
	params, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL test connection options: %w", err)
	}
	for key := range params {
		switch strings.ToLower(key) {
		case "database", "dbname", "service":
			return fmt.Errorf("PostgreSQL test database override %q is forbidden", key)
		}
	}
	return nil
}

// requireEvidenceMergeTestDatabase checks the actual session target before
// issuing any DDL or writes. Both independent pools must pass this check.
func requireEvidenceMergeTestDatabase(ctx context.Context, db *DB) error {
	var name string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return fmt.Errorf("verify PostgreSQL test database identity: %w", err)
	}
	if name != "devplane_ciqual" {
		return fmt.Errorf("refusing PostgreSQL test writes to database other than devplane_ciqual")
	}
	return nil
}

// Opt-in integration coverage against a disposable, isolated PostgreSQL
// database. Never point this at an existing application database.
// DEVPLANE_TEST_POSTGRES_URL must be a dedicated test database URL.
func TestPostgresEvidenceMergeRealConcurrentTransactions(t *testing.T) {
	dsn := os.Getenv("DEVPLANE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set DEVPLANE_TEST_POSTGRES_URL to a disposable PostgreSQL test database")
	}
	if err := validateEvidenceMergeTestDSN(dsn); err != nil {
		t.Fatal(err)
	}
	caseID := fmt.Sprintf("pg-qual-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := requireEvidenceMergeTestDatabase(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := requireEvidenceMergeTestDatabase(ctx, second); err != nil {
		t.Fatal(err)
	}
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

func TestEvidenceMergeTestDSNRejectsDatabaseOverrides(t *testing.T) {
	cases := []struct {
		name      string
		dsn       string
		wantValid bool
	}{
		{"dedicated database", "postgres://ci@127.0.0.1:55439/devplane_ciqual?sslmode=disable", true},
		{"postgresql scheme", "postgresql://ci@localhost/devplane_ciqual", true},
		{"wrong path", "postgres://ci@localhost/production", false},
		{"query database override", "postgres://ci@localhost/devplane_ciqual?database=production", false},
		{"encoded database override", "postgres://ci@localhost/devplane_ciqual?%64atabase=production", false},
		{"query dbname override", "postgres://ci@localhost/devplane_ciqual?dbname=production", false},
		{"query service override", "postgres://ci@localhost/devplane_ciqual?service=production", false},
		{"uppercase query override", "postgres://ci@localhost/devplane_ciqual?DATABASE=production", false},
		{"missing host", "postgres:///devplane_ciqual?sslmode=disable", false},
		{"non-postgres scheme", "file:///devplane_ciqual", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEvidenceMergeTestDSN(tc.dsn)
			if (err == nil) != tc.wantValid {
				t.Fatalf("valid=%t, want %t (err=%v)", err == nil, tc.wantValid, err)
			}
		})
	}
}
