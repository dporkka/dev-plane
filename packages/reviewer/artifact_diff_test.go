package reviewer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	_ "github.com/mattn/go-sqlite3"
)

func TestArtifactDiffSummaryUsesSemanticSpreadsheetDiff(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE workspace_snapshots (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			agent_run_id TEXT,
			artifact_version_digest TEXT,
			created_at DATETIME NOT NULL
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	store, err := artifactstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := artifactstore.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}

	before := reviewerSpreadsheetArtifact(t, store, "100")
	after := reviewerSpreadsheetArtifact(t, store, "125")
	beforeManifest, err := artifactstore.NewManifest([]artifactstore.Artifact{before})
	if err != nil {
		t.Fatal(err)
	}
	beforeManifestDesc, err := manager.PutManifest(context.Background(), beforeManifest)
	if err != nil {
		t.Fatal(err)
	}
	beforeVersionDesc, err := manager.PutVersion(context.Background(), artifactstore.Version{
		Schema: artifactstore.VersionSchemaV1,
		Manifest: beforeManifestDesc.Digest,
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	afterManifest, err := artifactstore.NewManifest([]artifactstore.Artifact{after})
	if err != nil {
		t.Fatal(err)
	}
	afterManifestDesc, err := manager.PutManifest(context.Background(), afterManifest)
	if err != nil {
		t.Fatal(err)
	}
	afterVersionDesc, err := manager.PutVersion(context.Background(), artifactstore.Version{
		Schema: artifactstore.VersionSchemaV1,
		Parents: []artifactstore.Digest{beforeVersionDesc.Digest},
		Manifest: afterManifestDesc.Digest,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO workspace_snapshots (id, workspace_id, artifact_version_digest, created_at)
			VALUES ('snap-before', 'workspace-1', ?, ?);
		INSERT INTO workspace_snapshots (id, workspace_id, agent_run_id, artifact_version_digest, created_at)
			VALUES ('snap-after', 'workspace-1', 'run-1', ?, ?);
	`, beforeVersionDesc.Digest.String(), now.Add(-time.Second), afterVersionDesc.Digest.String(), now)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReviewer(db, nil).WithArtifactManager(manager)
	summary, err := r.getArtifactDiffSummary(context.Background(), "run-1", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.FilesChanged != 1 || summary.Modifications != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Changes) != 1 {
		t.Fatalf("changes = %+v", summary.Changes)
	}
	change := summary.Changes[0]
	if change.Path != "budget.xlsx" || change.Status != "modified" || !change.Semantic {
		t.Fatalf("change = %+v", change)
	}
	if change.ChangeCount != 1 || len(change.Changes) != 1 {
		t.Fatalf("semantic change count = %+v", change)
	}
}

func reviewerSpreadsheetArtifact(t *testing.T, store artifactstore.BlobStore, value string) artifactstore.Artifact {
	t.Helper()
	semanticPayload, err := json.Marshal(artifactstore.SpreadsheetSemantic{
		Sheets: []artifactstore.SpreadsheetSheet{{
			Name: "Budget",
			Cells: []artifactstore.SpreadsheetCell{{Ref: "A1", Value: value}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	semanticDesc, err := store.Put(context.Background(), bytes.NewReader(semanticPayload))
	if err != nil {
		t.Fatal(err)
	}
	semanticDesc.MediaType = artifactstore.SemanticSpreadsheetMediaType
	original := []byte("xlsx:" + value)
	originalDesc, err := store.Put(context.Background(), bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	originalDesc.MediaType = artifactstore.MediaTypeXLSX
	semanticDigest := semanticDesc.Digest
	return artifactstore.Artifact{
		Path: "budget.xlsx",
		Kind: artifactstore.KindSpreadsheet,
		Descriptor: originalDesc,
		SemanticDigest: &semanticDigest,
		Derivatives: []artifactstore.DerivativeRef{{
			Role: "semantic-spreadsheet",
			Descriptor: semanticDesc,
		}},
	}
}
