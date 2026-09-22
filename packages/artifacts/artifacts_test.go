package artifacts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestLocalStoreDeduplicatesAndReads(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	payload := []byte("same immutable payload")

	first, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Size != second.Size {
		t.Fatalf("deduplicated descriptors differ: %+v %+v", first, second)
	}

	ok, err := store.Has(ctx, first.Digest)
	if err != nil || !ok {
		t.Fatalf("expected stored digest, ok=%v err=%v", ok, err)
	}
	reader, err := store.Open(ctx, first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %q", got)
	}
}

func TestManifestCanonicalOrdering(t *testing.T) {
	a := testArtifact("z/video.mp4", KindVideo, []byte("video"))
	b := testArtifact("a/spec.docx", KindDocument, []byte("doc"))

	first, err := NewManifest([]Artifact{a, b})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManifest([]Artifact{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifacts[0].Path != "a/spec.docx" {
		t.Fatalf("manifest was not sorted: %+v", first.Artifacts)
	}
	firstDigest, _ := first.Digest()
	secondDigest, _ := second.Digest()
	if firstDigest != secondDigest {
		t.Fatalf("canonical manifests differ: %s != %s", firstDigest, secondDigest)
	}
}

func TestManagerArtifactManifestVersionRoundTrip(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	artifact, err := manager.PutArtifact(ctx, PutArtifactRequest{
		Path:      "docs/proposal.docx",
		Kind:      KindDocument,
		MediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Metadata:  map[string]string{"title": "Proposal"},
		Reader:    bytes.NewReader([]byte("byte-exact original")),
	})
	if err != nil {
		t.Fatal(err)
	}

	var materialized bytes.Buffer
	if err := manager.Materialize(ctx, artifact, &materialized); err != nil {
		t.Fatal(err)
	}
	if materialized.String() != "byte-exact original" {
		t.Fatalf("unexpected materialized payload %q", materialized.String())
	}

	manifest, err := NewManifest([]Artifact{artifact})
	if err != nil {
		t.Fatal(err)
	}
	manifestDescriptor, err := manager.PutManifest(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	loadedManifest, err := manager.LoadManifest(ctx, manifestDescriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedManifest.Artifacts) != 1 || loadedManifest.Artifacts[0].Path != artifact.Path {
		t.Fatalf("unexpected loaded manifest: %+v", loadedManifest)
	}

	version := Version{
		Schema:    VersionSchemaV1,
		Manifest:  manifestDescriptor.Digest,
		Author:    ActorIdentity{ID: "agent-7", Type: "agent"},
		Message:   "revise proposal",
		CreatedAt: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC),
		Provenance: Provenance{
			TaskID:      "task-42",
			AgentID:     "agent-7",
			WorkspaceID: "workspace-9",
		},
	}
	versionDescriptor, err := manager.PutVersion(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	loadedVersion, err := manager.LoadVersion(ctx, versionDescriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if loadedVersion.Manifest != version.Manifest || loadedVersion.Message != version.Message {
		t.Fatalf("unexpected loaded version: %+v", loadedVersion)
	}
}

func TestNormalizeArtifactPathRejectsTraversal(t *testing.T) {
	for _, value := range []string{"", "/absolute/file", "../secret", "docs/../../secret"} {
		if _, err := NormalizeArtifactPath(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	normalized, err := NormalizeArtifactPath(`docs\proposal.docx`)
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "docs/proposal.docx" {
		t.Fatalf("unexpected normalized path %q", normalized)
	}
}

func TestLocalStoreMissingObject(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	missing := HashBytes([]byte("missing"))
	_, err = store.Open(context.Background(), missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func testArtifact(path string, kind Kind, payload []byte) Artifact {
	return Artifact{
		Path: path,
		Kind: kind,
		Descriptor: Descriptor{
			Digest: HashBytes(payload),
			Size:   int64(len(payload)),
		},
	}
}
