// Package contracttest provides reusable forge provider conformance tests.
package contracttest

import (
	"testing"

	"github.com/ai-dev-control-plane/forge"
)

// Fixture supplies a fresh provider and repository for one conformance run.
type Fixture struct {
	Provider   forge.Provider
	Credential forge.Credential
	Repository forge.Repository
}

// Factory returns a fresh isolated fixture.
type Factory func(t *testing.T) Fixture

// Run verifies the stable provider-neutral behavior expected by Dev Plane.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	fixture := factory(t)
	if fixture.Provider == nil {
		t.Fatal("provider is nil")
	}
	if fixture.Provider.Name() == "" {
		t.Fatal("provider Name() must not be empty")
	}

	opened, err := fixture.Provider.OpenChange(
		t.Context(),
		fixture.Credential,
		fixture.Repository,
		forge.OpenChangeRequest{
			Title: "Portable change",
			Body:  "Created by forge conformance tests.",
			Head:  "feature/portable-change",
			Base:  "main",
			Draft: true,
		},
	)
	if err != nil {
		t.Fatalf("OpenChange: %v", err)
	}
	if opened == nil {
		t.Fatal("OpenChange returned nil change")
	}
	if opened.Number <= 0 {
		t.Fatalf("change number = %d, want positive", opened.Number)
	}
	if opened.URL == "" {
		t.Fatal("change URL must not be empty")
	}
	if opened.State != forge.ChangeStateOpen {
		t.Fatalf("change state = %q, want open", opened.State)
	}
	if opened.Head != "feature/portable-change" || opened.Base != "main" {
		t.Fatalf("change branches = %q -> %q", opened.Head, opened.Base)
	}
	if !opened.Draft {
		t.Fatal("draft flag was not preserved")
	}

	merged, err := fixture.Provider.MergeChange(
		t.Context(),
		fixture.Credential,
		fixture.Repository,
		opened.Number,
		forge.MergeChangeRequest{Method: forge.MergeMethodSquash},
	)
	if err != nil {
		t.Fatalf("MergeChange: %v", err)
	}
	if merged == nil || !merged.Merged {
		t.Fatalf("MergeChange result = %#v, want merged", merged)
	}
	if merged.Revision == "" {
		t.Fatal("merged revision must not be empty")
	}

	if _, err := fixture.Provider.OpenChange(
		t.Context(),
		fixture.Credential,
		fixture.Repository,
		forge.OpenChangeRequest{Title: "", Head: "feature", Base: "main"},
	); !forge.IsInvalidRequest(err) {
		t.Fatalf("OpenChange invalid request error = %v", err)
	}

	if _, err := fixture.Provider.MergeChange(
		t.Context(),
		fixture.Credential,
		fixture.Repository,
		0,
		forge.MergeChangeRequest{},
	); !forge.IsInvalidRequest(err) {
		t.Fatalf("MergeChange invalid number error = %v", err)
	}
}
