package projectbrain

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeSource struct {
	name  string
	facts []Fact
	err   error
}

func (s fakeSource) Name() string { return s.name }
func (s fakeSource) Facts(context.Context, Query) ([]Fact, error) {
	return append([]Fact(nil), s.facts...), s.err
}

func fact(subject, relation, object, repo, revision string, scope Scope, source string, confidence float64) Fact {
	return Fact{
		Subject:    subject,
		Relation:   relation,
		Object:     object,
		Repository: repo,
		Revision:   revision,
		Scope:      scope,
		Provenance: []Provenance{{Source: source, Confidence: confidence}},
	}
}

func request() CompileRequest {
	return CompileRequest{
		Repository:         "dporkka/dev-plane",
		Revision:           "abc123",
		Objective:          "Fix invoice save failure",
		AcceptanceCriteria: []string{"failed invoice save preserves draft"},
		ChangedPaths:       []string{"apps/web/invoice/form.tsx"},
		MaxFacts:           10,
	}
}

func TestCompileFiltersToExactRevisionAndRepositoryScope(t *testing.T) {
	src := fakeSource{name: "graph", facts: []Fact{
		fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.9),
		fact("OldInvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "old456", ScopeRevision, "graph", 1.0),
		fact("Architecture", "requires", "exact-head verification", "dporkka/dev-plane", "", ScopeRepository, "graph", 0.8),
		fact("OtherRepo", "contains", "InvoiceForm", "other/repo", "abc123", ScopeRevision, "graph", 1.0),
	}}

	pkg, err := NewCompiler(src).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	got := make([]string, 0, len(pkg.Facts))
	for _, f := range pkg.Facts {
		got = append(got, f.Subject)
	}
	want := []string{"InvoiceForm", "Architecture"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("facts = %#v, want %#v", got, want)
	}
}

func TestCompileMergesEquivalentFactsAndPreservesProvenance(t *testing.T) {
	a := fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "gitnexus", 0.8)
	a.Provenance[0].Reference = "graph://invoice"
	b := fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "lsp", 0.95)
	b.Provenance[0].Reference = "lsp://form.tsx#saveInvoice"

	pkg, err := NewCompiler(
		fakeSource{name: "gitnexus", facts: []Fact{a}},
		fakeSource{name: "lsp", facts: []Fact{b}},
	).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(pkg.Facts) != 1 {
		t.Fatalf("len(Facts) = %d, want 1", len(pkg.Facts))
	}
	if got := len(pkg.Facts[0].Provenance); got != 2 {
		t.Fatalf("len(Provenance) = %d, want 2", got)
	}
	if pkg.Facts[0].Provenance[0].Source != "gitnexus" || pkg.Facts[0].Provenance[1].Source != "lsp" {
		t.Fatalf("provenance order = %#v", pkg.Facts[0].Provenance)
	}
}

func TestCompileRanksTaskRelevantFactsBeforeUnrelatedHigherConfidenceFacts(t *testing.T) {
	src := fakeSource{name: "graph", facts: []Fact{
		fact("BillingCache", "uses", "Redis", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 1.0),
		fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.6),
	}}

	pkg, err := NewCompiler(src).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(pkg.Facts) != 2 || pkg.Facts[0].Subject != "InvoiceForm" {
		t.Fatalf("ranked facts = %#v, want InvoiceForm first", pkg.Facts)
	}
}

func TestCompileHonorsFactBudgetDeterministically(t *testing.T) {
	req := request()
	req.MaxFacts = 2
	src := fakeSource{name: "graph", facts: []Fact{
		fact("Zeta", "mentions", "invoice", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.5),
		fact("Alpha", "mentions", "invoice", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.5),
		fact("Beta", "mentions", "invoice", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.5),
	}}

	pkg, err := NewCompiler(src).Compile(context.Background(), req)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got := []string{pkg.Facts[0].Subject, pkg.Facts[1].Subject}; !reflect.DeepEqual(got, []string{"Alpha", "Beta"}) {
		t.Fatalf("facts = %#v, want [Alpha Beta]", got)
	}
}

func TestCompileContinuesWithExplicitWarningWhenSourceFails(t *testing.T) {
	pkg, err := NewCompiler(
		fakeSource{name: "gitnexus", err: errors.New("index unavailable")},
		fakeSource{name: "fallback", facts: []Fact{
			fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "fallback", 0.7),
		}},
	).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(pkg.Facts) != 1 {
		t.Fatalf("len(Facts) = %d, want 1", len(pkg.Facts))
	}
	if len(pkg.Warnings) != 1 || pkg.Warnings[0].Source != "gitnexus" {
		t.Fatalf("Warnings = %#v, want gitnexus warning", pkg.Warnings)
	}
}

func TestCompileDigestIsStableAcrossSourceOrderAndObservationTime(t *testing.T) {
	first := fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "gitnexus", 0.9)
	first.Provenance[0].Reference = "graph://invoice"
	first.Provenance[0].ObservedAt = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	second := fact("InvoiceService", "tested_by", "invoice_test", "dporkka/dev-plane", "abc123", ScopeRevision, "tests", 0.8)

	pkgA, err := NewCompiler(
		fakeSource{name: "one", facts: []Fact{first}},
		fakeSource{name: "two", facts: []Fact{second}},
	).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile(A) error = %v", err)
	}

	first.Provenance[0].ObservedAt = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	pkgB, err := NewCompiler(
		fakeSource{name: "two", facts: []Fact{second}},
		fakeSource{name: "one", facts: []Fact{first}},
	).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile(B) error = %v", err)
	}

	if pkgA.Digest == "" || pkgA.Digest != pkgB.Digest {
		t.Fatalf("digest A=%q B=%q, want stable non-empty digest", pkgA.Digest, pkgB.Digest)
	}
}

func TestCompileDigestChangesWhenFactMeaningChanges(t *testing.T) {
	srcA := fakeSource{name: "graph", facts: []Fact{
		fact("InvoiceForm", "calls", "saveInvoice", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.9),
	}}
	srcB := fakeSource{name: "graph", facts: []Fact{
		fact("InvoiceForm", "calls", "saveInvoiceSafely", "dporkka/dev-plane", "abc123", ScopeRevision, "graph", 0.9),
	}}

	pkgA, err := NewCompiler(srcA).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile(A) error = %v", err)
	}
	pkgB, err := NewCompiler(srcB).Compile(context.Background(), request())
	if err != nil {
		t.Fatalf("Compile(B) error = %v", err)
	}
	if pkgA.Digest == pkgB.Digest {
		t.Fatalf("digest did not change after semantic fact change: %q", pkgA.Digest)
	}
}
