package artifacts

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestChunkedArtifactRoundTrip(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	chunker, err := NewContentDefinedChunker(ChunkingConfig{
		MinSize: 64, AverageSize: 128, MaxSize: 256,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := deterministicPayload(32 << 10)
	artifact, err := manager.PutChunkedArtifact(context.Background(), chunker, PutArtifactRequest{
		Path:      "media/interview.mp4",
		Kind:      KindVideo,
		MediaType: "video/mp4",
		Reader:    bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Chunks) < 2 {
		t.Fatalf("chunks = %d, want multiple chunks", len(artifact.Chunks))
	}
	if artifact.Descriptor.Digest != HashBytes(payload) {
		t.Fatalf("full digest = %s, want %s", artifact.Descriptor.Digest, HashBytes(payload))
	}

	var restored bytes.Buffer
	if err := manager.Materialize(context.Background(), artifact, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), payload) {
		t.Fatal("chunked materialization changed payload bytes")
	}

	// The full object is intentionally not required to exist as one CAS blob.
	fullExists, err := store.Has(context.Background(), artifact.Descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if fullExists {
		t.Fatal("chunked upload unexpectedly stored full payload as a duplicate blob")
	}
}

func TestContentDefinedChunksReuseAfterInsertion(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chunker, err := NewContentDefinedChunker(ChunkingConfig{
		MinSize: 128, AverageSize: 256, MaxSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := deterministicPayload(128 << 10)
	insertAt := 1700
	insert := []byte("inserted-content-that-should-only-disturb-nearby-chunks")
	modified := make([]byte, 0, len(original)+len(insert))
	modified = append(modified, original[:insertAt]...)
	modified = append(modified, insert...)
	modified = append(modified, original[insertAt:]...)

	_, before, err := chunker.Put(context.Background(), store, bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := chunker.Put(context.Background(), store, bytes.NewReader(modified))
	if err != nil {
		t.Fatal(err)
	}

	beforeSet := make(map[Digest]struct{}, len(before))
	for _, chunk := range before {
		beforeSet[chunk.Descriptor.Digest] = struct{}{}
	}
	shared := 0
	for _, chunk := range after {
		if _, ok := beforeSet[chunk.Descriptor.Digest]; ok {
			shared++
		}
	}
	if shared < len(before)/2 {
		t.Fatalf("shared chunks = %d/%d; content-defined chunker did not resynchronize", shared, len(before))
	}
}

func TestChunkingConfigValidation(t *testing.T) {
	for _, config := range []ChunkingConfig{
		{},
		{MinSize: 256, AverageSize: 128, MaxSize: 512},
		{MinSize: 64, AverageSize: 192, MaxSize: 512},
	} {
		if _, err := NewContentDefinedChunker(config); err == nil {
			t.Fatalf("expected invalid config %+v to fail", config)
		}
	}
}

func deterministicPayload(size int) []byte {
	data := make([]byte, size)
	var state uint64 = 0x123456789abcdef0
	for i := range data {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		data[i] = byte(state)
	}
	// Add stable low-entropy regions resembling real media/document payloads.
	for start := 4096; start+512 < len(data); start += 8192 {
		copy(data[start:start+512], bytes.Repeat([]byte{byte(start >> 8)}, 512))
	}
	return data
}

var _ io.Reader = (*bytes.Reader)(nil)
