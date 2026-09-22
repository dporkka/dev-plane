package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const (
	ManifestMediaType = "application/vnd.dev-plane.artifact-manifest.v1+json"
	VersionMediaType  = "application/vnd.dev-plane.artifact-version.v1+json"
)

type Manager struct {
	store BlobStore
}

func NewManager(store BlobStore) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("artifact blob store is required")
	}
	return &Manager{store: store}, nil
}

type PutArtifactRequest struct {
	Path      string
	Kind      Kind
	MediaType string
	Metadata  map[string]string
	Reader    io.Reader
}

func (m *Manager) PutArtifact(ctx context.Context, req PutArtifactRequest) (Artifact, error) {
	normalizedPath, err := NormalizeArtifactPath(req.Path)
	if err != nil {
		return Artifact{}, err
	}
	if !req.Kind.Valid() {
		return Artifact{}, fmt.Errorf("invalid artifact kind %q", req.Kind)
	}
	descriptor, err := m.store.Put(ctx, req.Reader)
	if err != nil {
		return Artifact{}, err
	}
	descriptor.MediaType = req.MediaType
	artifact := Artifact{
		Path:       normalizedPath,
		Kind:       req.Kind,
		Descriptor: descriptor,
		Metadata:   cloneStringMap(req.Metadata),
	}
	if err := artifact.Validate(); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// PutChunkedArtifact stores large payloads as independently-addressed chunks.
// The Artifact descriptor still identifies the exact full byte stream, while
// Chunks describes how to reconstruct it without storing a duplicate full blob.
func (m *Manager) PutChunkedArtifact(ctx context.Context, chunker *ContentDefinedChunker, req PutArtifactRequest) (Artifact, error) {
	if chunker == nil {
		return Artifact{}, fmt.Errorf("content-defined chunker is required")
	}
	normalizedPath, err := NormalizeArtifactPath(req.Path)
	if err != nil {
		return Artifact{}, err
	}
	if !req.Kind.Valid() {
		return Artifact{}, fmt.Errorf("invalid artifact kind %q", req.Kind)
	}
	descriptor, chunks, err := chunker.Put(ctx, m.store, req.Reader)
	if err != nil {
		return Artifact{}, err
	}
	descriptor.MediaType = req.MediaType
	artifact := Artifact{
		Path:       normalizedPath,
		Kind:       req.Kind,
		Descriptor: descriptor,
		Chunks:     chunks,
		Metadata:   cloneStringMap(req.Metadata),
	}
	if err := artifact.Validate(); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func (m *Manager) Materialize(ctx context.Context, artifact Artifact, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("artifact destination is required")
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	if len(artifact.Chunks) > 0 {
		for i, chunk := range artifact.Chunks {
			src, err := m.store.Open(ctx, chunk.Descriptor.Digest)
			if err != nil {
				return fmt.Errorf("open artifact %q chunk %d: %w", artifact.Path, i, err)
			}
			_, copyErr := io.Copy(dst, contextReader{ctx: ctx, r: src})
			closeErr := src.Close()
			if copyErr != nil {
				return fmt.Errorf("materialize artifact %q chunk %d: %w", artifact.Path, i, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close artifact %q chunk %d: %w", artifact.Path, i, closeErr)
			}
		}
		return nil
	}

	src, err := m.store.Open(ctx, artifact.Descriptor.Digest)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := io.Copy(dst, contextReader{ctx: ctx, r: src}); err != nil {
		return fmt.Errorf("materialize artifact %q: %w", artifact.Path, err)
	}
	return nil
}

func (m *Manager) PutManifest(ctx context.Context, manifest Manifest) (Descriptor, error) {
	payload, err := manifest.CanonicalJSON()
	if err != nil {
		return Descriptor{}, err
	}
	descriptor, err := m.store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		return Descriptor{}, err
	}
	descriptor.MediaType = ManifestMediaType
	return descriptor, nil
}

func (m *Manager) LoadManifest(ctx context.Context, digest Digest) (Manifest, error) {
	reader, err := m.store.Open(ctx, digest)
	if err != nil {
		return Manifest{}, err
	}
	defer reader.Close()
	payload, err := io.ReadAll(contextReader{ctx: ctx, r: reader})
	if err != nil {
		return Manifest{}, fmt.Errorf("read artifact manifest: %w", err)
	}
	return DecodeManifest(payload)
}

func (m *Manager) PutVersion(ctx context.Context, version Version) (Descriptor, error) {
	payload, err := version.CanonicalJSON()
	if err != nil {
		return Descriptor{}, err
	}
	descriptor, err := m.store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		return Descriptor{}, err
	}
	descriptor.MediaType = VersionMediaType
	return descriptor, nil
}

func (m *Manager) LoadVersion(ctx context.Context, digest Digest) (Version, error) {
	reader, err := m.store.Open(ctx, digest)
	if err != nil {
		return Version{}, err
	}
	defer reader.Close()
	payload, err := io.ReadAll(contextReader{ctx: ctx, r: reader})
	if err != nil {
		return Version{}, fmt.Errorf("read artifact version: %w", err)
	}
	var version Version
	if err := json.Unmarshal(payload, &version); err != nil {
		return Version{}, fmt.Errorf("decode artifact version: %w", err)
	}
	if _, err := version.CanonicalJSON(); err != nil {
		return Version{}, err
	}
	return version, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
