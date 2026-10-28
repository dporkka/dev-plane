package db

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func testEvidenceStore(t *testing.T) *DB {
	t.Helper()
	db, err := New("file:evidence-merge-" + strings.ReplaceAll(t.Name(), "/", "-") + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RunMigrations("migrations"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func evidence(work, head, base string, gates ...repoprotocol.GateEvidence) repoprotocol.EvidenceBundle {
	return repoprotocol.EvidenceBundle{WorkItemID: work, HeadSHA: head, BaseSHA: base, Gates: gates}
}
func gate(name string) repoprotocol.GateEvidence {
	return repoprotocol.GateEvidence{Name: name, Status: repoprotocol.GatePending}
}

func TestMergeEvidenceGatesPreservesSiblingGatesAndOrder(t *testing.T) {
	db := testEvidenceStore(t)
	ctx := context.Background()
	first := evidence("task-1", "head-1", "base-1", gate("unit"))
	second := evidence("task-1", "head-1", "base-1", gate("remote"))
	if err := db.MergeEvidenceGates(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := db.MergeEvidenceGates(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetEvidenceBundle(ctx, "task-1", "head-1")
	if err != nil {
		t.Fatal(err)
	}
	want := append(first.Gates, second.Gates...)
	if !reflect.DeepEqual(got.Gates, want) {
		t.Fatalf("overwritten sibling evidence: %+v", got)
	}
}

func TestMergeEvidenceGatesIsIdempotentAndRejectsConflicts(t *testing.T) {
	db := testEvidenceStore(t)
	ctx := context.Background()
	original := evidence("task-1", "head-1", "base-1", gate("unit"))
	if err := db.MergeEvidenceGates(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := db.MergeEvidenceGates(ctx, original); err != nil {
		t.Fatal(err)
	}
	changed := evidence("task-1", "head-1", "base-1", repoprotocol.GateEvidence{Name: "unit", Status: repoprotocol.GatePassed})
	if err := db.MergeEvidenceGates(ctx, changed); err == nil {
		t.Fatal("overwrote existing gate result")
	}
	if err := db.MergeEvidenceGates(ctx, evidence("task-1", "head-1", "other", gate("remote"))); err == nil {
		t.Fatal("base SHA changed")
	}
	got, err := db.GetEvidenceBundle(ctx, "task-1", "head-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("mutation despite conflict: %#v", got)
	}
}

func TestMergeEvidenceGatesRejectsInvalidAndDuplicateIncoming(t *testing.T) {
	db := testEvidenceStore(t)
	ctx := context.Background()
	for _, tc := range []repoprotocol.EvidenceBundle{
		evidence("task-1", "head-1", "base-1"),
		evidence("task-1", "head-1", "base-1", gate("a"), gate("a")),
		evidence("task-1", "head-1", "base-1", gate("")),
	} {
		if err := db.MergeEvidenceGates(ctx, tc); err == nil {
			t.Fatalf("accepted invalid bundle: %+v", tc)
		}
	}
}

func TestMergeEvidenceGatesConcurrentWritersKeepAllResults(t *testing.T) {
	db := testEvidenceStore(t)
	ctx := context.Background()
	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := string(rune('A' + n))
			errs <- db.MergeEvidenceGates(ctx, evidence("task-1", "head-1", "base-1", gate(name)))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update lost or failed: %v", err)
		}
	}
	got, err := db.GetEvidenceBundle(ctx, "task-1", "head-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Gates) != count {
		t.Fatalf("lost gates: got %d expected %d: %+v", len(got.Gates), count, got)
	}
}

func TestMergeEvidenceGatesIndependentSQLiteConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proof.db")
	left, err := New(path + "?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := New(path + "?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if err := left.RunMigrations("migrations"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, tc := range []struct {
		store *DB
		name  string
	}{
		{left, "build"}, {right, "test"},
	} {
		wg.Add(1)
		go func(store *DB, name string) {
			defer wg.Done()
			errs <- store.MergeEvidenceGates(context.Background(), evidence("task-3", "head-3", "base-3", gate(name)))
		}(tc.store, tc.name)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("cross-connection merge failed: %v", err)
		}
	}
	got, err := right.GetEvidenceBundle(context.Background(), "task-3", "head-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Gates) != 2 {
		t.Fatalf("lost cross-connection evidence: %+v", got)
	}
}
