package artifacts

import (
	"context"
	"errors"
	"io"
)

var ErrNotFound = errors.New("artifact blob not found")

// BlobStore stores immutable payload bytes by content digest. Implementations
// must be idempotent: storing the same bytes more than once returns the same
// descriptor and must not create distinct logical objects.
type BlobStore interface {
	Put(ctx context.Context, r io.Reader) (Descriptor, error)
	Open(ctx context.Context, digest Digest) (io.ReadCloser, error)
	Has(ctx context.Context, digest Digest) (bool, error)
}
