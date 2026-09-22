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
		CreatedAt: time.Date(2026, 9, 22, 9, 0, 0, 0, time.FixedZone("BRT", -3*60*60)),
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

	utcVersion := version
	utcVersion.CreatedAt = version.CreatedAt.UTC()
	firstDigest, err := version.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := utcVersion.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("same instant produced different version digest: %s != %s", firstDigest, secondDigest)
	}
}

func TestChunkCoverageValidation(t *testing.T) {
	payload := []byte("abcdefgh")
	artifact := testArtifact("video/clip.mp4", KindVideo, payload)
	artifact.Chunks = []ChunkRef{
		{Descriptor: Descriptor{Digest: HashBytes([]byte("abcd")), Size: 4}, Offset: 0},
		{Descriptor: Descriptor{Digest: HashBytes([]byte("efgh")), Size: 4}, Offset: 4},
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("valid chunks rejected: %v", err)
	}

	artifact.Chunks[1].Offset = 5
	if err := artifact.Validate(); err == nil {
		t.Fatal("expected chunk gap to be rejected")
	}
}

func TestGitLFSPointerRoundTrip(t *testing.T) {
	descriptor := Descriptor{Digest: HashBytes([]byte("large media")), Size: int64(len("large media"))}
	pointer, err := NewLFSPointer(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := pointer.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseLFSPointer(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != pointer {
		t.Fatalf("pointer mismatch: %+v != %+v", parsed, pointer)
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
