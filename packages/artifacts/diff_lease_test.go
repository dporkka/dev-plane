package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDiffArtifactsDocumentSemanticChange(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	before := semanticDocumentArtifact(t, store, "before", []string{"Title", "Annual cost: $24,000", "Terms"})
	after := semanticDocumentArtifact(t, store, "after", []string{"Title", "Annual cost: $21,000", "Terms"})

	diff, err := manager.DiffArtifacts(context.Background(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Changed || !diff.Semantic {
		t.Fatalf("diff = %+v, want changed semantic diff", diff)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Operation != DiffModified {
		t.Fatalf("changes = %+v, want one modification", diff.Changes)
	}
	if diff.Changes[0].Before != "Annual cost: $24,000" ||
		diff.Changes[0].After != "Annual cost: $21,000" {
		t.Fatalf("unexpected document change: %+v", diff.Changes[0])
	}
}

func TestDiffSequenceDetectsInsertion(t *testing.T) {
	changes := diffSequence(
		[]string{"A", "B", "C"},
		[]string{"A", "inserted", "B", "C"},
	)
	if len(changes) != 1 || changes[0].Operation != DiffAdded || changes[0].After != "inserted" {
		t.Fatalf("changes = %+v, want one insertion", changes)
	}
}

func TestDiffArtifactsImageMetadata(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	before := semanticImageArtifact(t, store, "before", ImageSemantic{Width: 1200, Height: 800, Format: "png"})
	after := semanticImageArtifact(t, store, "after", ImageSemantic{Width: 1600, Height: 900, Format: "png"})

	diff, err := manager.DiffArtifacts(context.Background(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Semantic {
		t.Fatalf("diff = %+v, want semantic image diff", diff)
	}
	if diff.MetadataChanges["width"].Before != "1200" || diff.MetadataChanges["width"].After != "1600" {
		t.Fatalf("width diff = %+v", diff.MetadataChanges["width"])
	}
	if diff.MetadataChanges["height"].Before != "800" || diff.MetadataChanges["height"].After != "900" {
		t.Fatalf("height diff = %+v", diff.MetadataChanges["height"])
	}
}

func TestDefaultCollaborationPolicy(t *testing.T) {
	text := Artifact{
		Path: "README.md",
		Kind: KindText,
		Descriptor: Descriptor{Digest: HashBytes([]byte("text")), Size: 4, MediaType: "text/markdown"},
	}
	textPolicy := DefaultCollaborationPolicy(text)
	if textPolicy.LeaseRequired || textPolicy.MergeStrategy != MergeTextThreeWay {
		t.Fatalf("text policy = %+v", textPolicy)
	}

	video := Artifact{
		Path: "launch.mp4",
		Kind: KindVideo,
		Descriptor: Descriptor{Digest: HashBytes([]byte("video")), Size: 5, MediaType: "video/mp4"},
	}
	videoPolicy := DefaultCollaborationPolicy(video)
	if !videoPolicy.LeaseRequired || !videoPolicy.ForkAllowed || videoPolicy.MergeStrategy != MergeExclusiveLease {
		t.Fatalf("video policy = %+v", videoPolicy)
	}
}

func TestMemoryLeaseStoreContentionRenewRelease(t *testing.T) {
	store := NewMemoryLeaseStore()
	now := time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	first, err := store.Acquire(context.Background(), AcquireLeaseRequest{
		ScopeID: "repo-1", Path: "media/hero.psd", OwnerID: "agent-1", TTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || first.Token == "" {
		t.Fatalf("lease = %+v", first)
	}

	if err := store.Validate(context.Background(), first); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	visible, err := store.Get(context.Background(), "repo-1", "media/hero.psd")
	if err != nil {
		t.Fatal(err)
	}
	if visible.Token != "" {
		t.Fatalf("Get() leaked lease token %q", visible.Token)
	}
	stale := first
	stale.Token = "wrong-token"
	if err := store.Validate(context.Background(), stale); !errors.Is(err, ErrLeaseToken) {
		t.Fatalf("Validate(stale) error = %v, want ErrLeaseToken", err)
	}

	_, err = store.Acquire(context.Background(), AcquireLeaseRequest{
		ScopeID: "repo-1", Path: "media/hero.psd", OwnerID: "agent-2", TTL: 30 * time.Minute,
	})
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second acquire error = %v, want ErrLeaseHeld", err)
	}

	now = now.Add(10 * time.Minute)
	renewed, err := store.Renew(context.Background(), first, 45*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Token != first.Token || !renewed.ExpiresAt.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("renewed lease = %+v", renewed)
	}
	if err := store.Release(context.Background(), renewed); err != nil {
		t.Fatal(err)
	}

	second, err := store.Acquire(context.Background(), AcquireLeaseRequest{
		ScopeID: "repo-1", Path: "media/hero.psd", OwnerID: "agent-2", TTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 {
		t.Fatalf("generation = %d, want 2", second.Generation)
	}
}

func TestMemoryLeaseStoreExpiryRejectsStaleToken(t *testing.T) {
	store := NewMemoryLeaseStore()
	now := time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	first, err := store.Acquire(context.Background(), AcquireLeaseRequest{
		ScopeID: "repo-1", Path: "video/demo.mp4", OwnerID: "agent-1", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Get(context.Background(), "repo-1", "video/demo.mp4"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Get() error = %v, want ErrLeaseExpired", err)
	}
	second, err := store.Acquire(context.Background(), AcquireLeaseRequest{
		ScopeID: "repo-1", Path: "video/demo.mp4", OwnerID: "agent-2", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("generation did not advance: first=%d second=%d", first.Generation, second.Generation)
	}
	if _, err := store.Renew(context.Background(), first, time.Minute); !errors.Is(err, ErrLeaseToken) {
		t.Fatalf("stale renew error = %v, want ErrLeaseToken", err)
	}
}

func TestLeaseValidation(t *testing.T) {
	store := NewMemoryLeaseStore()
	for _, req := range []AcquireLeaseRequest{
		{Path: "a", OwnerID: "owner", TTL: time.Minute},
		{ScopeID: "repo", Path: "../escape", OwnerID: "owner", TTL: time.Minute},
		{ScopeID: "repo", Path: "a", TTL: time.Minute},
		{ScopeID: "repo", Path: "a", OwnerID: "owner", TTL: 0},
		{ScopeID: "repo", Path: "a", OwnerID: "owner", TTL: MaxLeaseTTL + time.Second},
	} {
		if _, err := store.Acquire(context.Background(), req); err == nil {
			t.Fatalf("expected invalid lease request %+v to fail", req)
		}
	}
}

func semanticDocumentArtifact(t *testing.T, store BlobStore, identity string, paragraphs []string) Artifact {
	t.Helper()
	semantic := DocumentSemantic{}
	for _, paragraph := range paragraphs {
		semantic.Paragraphs = append(semantic.Paragraphs, DocumentParagraph{Text: paragraph})
	}
	payload, err := json.Marshal(semantic)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Put(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	descriptor.MediaType = SemanticDocumentMediaType
	digest := descriptor.Digest
	return Artifact{
		Path: "proposal.docx",
		Kind: KindDocument,
		Descriptor: Descriptor{
			Digest: HashBytes([]byte(identity)),
			Size: int64(len(identity)),
			MediaType: MediaTypeDOCX,
		},
		SemanticDigest: &digest,
		Derivatives: []DerivativeRef{{Role: "semantic-document", Descriptor: descriptor}},
	}
}

func semanticImageArtifact(t *testing.T, store BlobStore, identity string, semantic ImageSemantic) Artifact {
	t.Helper()
	payload, err := json.Marshal(semantic)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Put(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	descriptor.MediaType = SemanticImageMediaType
	digest := descriptor.Digest
	preview := Descriptor{
		Digest: HashBytes([]byte("preview-" + identity)),
		Size: int64(len("preview-" + identity)),
		MediaType: "image/png",
	}
	return Artifact{
		Path: "hero.png",
		Kind: KindImage,
		Descriptor: Descriptor{
			Digest: HashBytes([]byte(identity)),
			Size: int64(len(identity)),
			MediaType: "image/png",
		},
		SemanticDigest: &digest,
		Derivatives: []DerivativeRef{
			{Role: "semantic-image", Descriptor: descriptor},
			{Role: "preview-thumbnail", Descriptor: preview},
		},
	}
}

