package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalStore is a filesystem CAS intended for development, self-hosted nodes,
// and node-local worker caches. Cloud backends implement the same BlobStore
// interface.
type LocalStore struct {
	root string
}

func NewLocalStore(root string) (*LocalStore, error) {
	if root == "" {
		return nil, fmt.Errorf("artifact store root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact store root: %w", err)
	}
	return &LocalStore{root: root}, nil
}

func (s *LocalStore) Put(ctx context.Context, r io.Reader) (Descriptor, error) {
	if r == nil {
		return Descriptor{}, fmt.Errorf("artifact reader is required")
	}
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}

	tmpDir := filepath.Join(s.root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Descriptor{}, fmt.Errorf("create artifact temp directory: %w", err)
	}

	tmp, err := os.CreateTemp(tmpDir, "blob-*")
	if err != nil {
		return Descriptor{}, fmt.Errorf("create artifact temp file: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), contextReader{ctx: ctx, r: r})
	if copyErr != nil {
		_ = tmp.Close()
		return Descriptor{}, fmt.Errorf("write artifact blob: %w", copyErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Descriptor{}, fmt.Errorf("sync artifact blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Descriptor{}, fmt.Errorf("close artifact blob: %w", err)
	}

	digest := Digest{Algorithm: AlgorithmSHA256, Hex: hex.EncodeToString(hasher.Sum(nil))}
	target, err := s.objectPath(digest)
	if err != nil {
		return Descriptor{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Descriptor{}, fmt.Errorf("create artifact object directory: %w", err)
	}

	if _, err := os.Stat(target); err == nil {
		return Descriptor{Digest: digest, Size: size}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Descriptor{}, fmt.Errorf("stat artifact object: %w", err)
	}

	if err := os.Rename(tmpPath, target); err != nil {
		// Another writer may have won the race for the same immutable digest.
		if _, statErr := os.Stat(target); statErr == nil {
			return Descriptor{Digest: digest, Size: size}, nil
		}
		return Descriptor{}, fmt.Errorf("publish artifact object: %w", err)
	}
	keep = true
	return Descriptor{Digest: digest, Size: size}, nil
}

func (s *LocalStore) Open(ctx context.Context, digest Digest) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := s.objectPath(digest)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open artifact blob: %w", err)
	}
	return file, nil
}

func (s *LocalStore) Has(ctx context.Context, digest Digest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	target, err := s.objectPath(digest)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(target)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat artifact blob: %w", err)
}

func (s *LocalStore) objectPath(digest Digest) (string, error) {
	if !digest.Valid() {
		return "", fmt.Errorf("invalid artifact digest %q", digest.String())
	}
	return filepath.Join(s.root, digest.Algorithm, digest.Hex[:2], digest.Hex[2:]), nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}
