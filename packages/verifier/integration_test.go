package verifier

import (
	"context"
	"testing"

	dbpkg "github.com/ai-dev-control-plane/db"
	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

func TestVerifyPersistsEvidenceThroughDatabaseStore(t *testing.T) {
	database, err := dbpkg.New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("../db/migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {Stdout: "changed ok", ExitCode: 0},
		},
		errors: map[string]error{},
	}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	result, err := New(runtime, database).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	stored, err := database.GetEvidenceBundle(context.Background(), item.ID, "head456")
	if err != nil {
		t.Fatalf("GetEvidenceBundle() error = %v", err)
	}
	if stored.HeadSHA != result.Evidence.HeadSHA || stored.WorkItemID != result.Evidence.WorkItemID {
		t.Fatalf("stored evidence = %#v, result = %#v", stored, result.Evidence)
	}
	if err := stored.ValidateHead("head456"); err != nil {
		t.Fatalf("stored ValidateHead() error = %v", err)
	}
	if stored.Gates[0].Status != repoprotocol.GatePassed {
		t.Fatalf("stored gate status = %s", stored.Gates[0].Status)
	}
}
