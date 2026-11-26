package verifier

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/db"
	rp "github.com/ai-dev-control-plane/repoprotocol"
)

func TestControllerCILedgerLifecycle(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "ci.db")
	if v := os.Getenv("DEVPLANE_CI_LEDGER_TEST_POSTGRES"); v != "" {
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "postgres" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User == nil || u.User.String() != "fixture" || u.Path != "/devplane_ci_ledger_test" || u.RawQuery != "sslmode=disable" || u.Fragment != "" {
			t.Fatal("disposable loopback PostgreSQL fixture required")
		}
		dsn = v
	}
	database, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if database.Driver == "postgres" {
		var tables int
		if err := database.QueryRow("SELECT COUNT(*) FROM pg_tables WHERE schemaname='public'").Scan(&tables); err != nil || tables != 0 {
			t.Fatal("fixture database must be empty")
		}
	}
	if err := database.RunMigrations("../db/migrations"); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := rp.RemoteCIPolicy{Provider: "dev-plane-ci", Repository: "gitea:https://forge.example#77", DefinitionSHA256: strings.Repeat("d", 64), SignerKeyID: "test-key", PublicKey: pub, PipelineID: "pipeline", RunID: "run", JobID: "build", AttemptID: "1"}
	q := RemoteCIQuery{HeadSHA: strings.Repeat("a", 40), PipelineID: p.PipelineID, RunID: p.RunID, JobID: p.JobID, AttemptID: p.AttemptID}
	ledger := ControllerCILedger{DB: database.DB, Policy: p}
	attestor := RemoteCIAttestor{Source: ledger, Policy: p, PrivateKey: priv}
	if _, err := attestor.Attest(ctx, q); err == nil {
		t.Fatal("missing attempt signed")
	}
	if err := ledger.Enqueue(ctx, q); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Enqueue(ctx, q); err == nil {
		t.Fatal("duplicate queue accepted")
	}
	if err := ledger.Start(ctx, q, "worker", "fake"); err == nil {
		t.Fatal("start before assignment")
	}
	pending, err := attestor.Attest(ctx, q)
	if err != nil || pending.Verify(q.HeadSHA, p) == nil {
		t.Fatal("queued receipt eligible")
	}
	other, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	competitor := ControllerCILedger{DB: other.DB, Policy: p}
	type assignment struct {
		token string
		err   error
	}
	results := make(chan assignment, 2)
	start := make(chan struct{})
	for _, candidate := range []ControllerCILedger{ledger, competitor} {
		go func(l ControllerCILedger) {
			<-start
			token, err := l.Assign(ctx, q, "worker")
			results <- assignment{token, err}
		}(candidate)
	}
	close(start)
	lease := ""
	wins := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			lease = result.token
			wins++
		}
	}
	if wins != 1 || lease == "" {
		t.Fatalf("concurrent assignments won %d times", wins)
	}

	if _, err := ledger.Assign(ctx, q, "other"); err == nil {
		t.Fatal("assignment replay")
	}
	if err := ledger.Finish(ctx, q, "worker", lease, 0); err == nil {
		t.Fatal("finish before start")
	}
	if err := ledger.Start(ctx, q, "other", lease); err == nil {
		t.Fatal("foreign worker accepted")
	}
	if err := ledger.Start(ctx, q, "worker", "fake"); err == nil {
		t.Fatal("forged lease accepted")
	}
	if err := ledger.Start(ctx, q, "worker", lease); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Start(ctx, q, "worker", lease); err == nil {
		t.Fatal("replayed start accepted")
	}
	if err := ledger.Finish(ctx, q, "worker", lease, 0); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(ctx, q, "worker", lease, 1); err == nil {
		t.Fatal("terminal verdict overwritten")
	}
	receipt, err := attestor.Attest(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Verify(q.HeadSHA, p); err != nil {
		t.Fatal(err)
	}
	wrong := q
	wrong.HeadSHA = strings.Repeat("b", 40)
	if _, err := attestor.Attest(ctx, wrong); err == nil {
		t.Fatal("different head accepted")
	}
	wrong = q
	wrong.AttemptID = "2"
	if _, err := attestor.Attest(ctx, wrong); err == nil {
		t.Fatal("different attempt accepted")
	}
	// Explicitly record a separate failing attempt; a successful old attempt
	// must not satisfy the new current attempt policy.
	p.AttemptID = "2"
	q.AttemptID = "2"
	ledger.Policy = p
	attestor.Source = ledger
	attestor.Policy = p
	if err := ledger.Enqueue(ctx, q); err != nil {
		t.Fatal(err)
	}
	lease, err = ledger.Assign(ctx, q, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Start(ctx, q, "worker", lease); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(ctx, q, "worker", lease, 1); err != nil {
		t.Fatal(err)
	}
	failed, err := attestor.Attest(ctx, q)
	if err != nil || failed.Verify(q.HeadSHA, p) == nil || receipt.Verify(q.HeadSHA, p) == nil {
		t.Fatal("failure or old attempt eligible")
	}
}
