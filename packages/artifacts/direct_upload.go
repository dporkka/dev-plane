package artifacts

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrDirectUploadUnsupported = errors.New("direct multipart upload is not supported by this artifact store")

const (
	MinMultipartPartSize     int64 = 5 << 20
	DefaultMultipartPartSize int64 = 64 << 20
	MaxMultipartPartSize     int64 = 5 << 30
	MaxMultipartParts              = 10000
	DefaultPresignTTL               = 15 * time.Minute
)

type PresignedPart struct {
	PartNumber int       `json:"part_number"`
	URL        string    `json:"url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type CompletedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type DirectMultipartStore interface {
	BeginMultipart(ctx context.Context, stagingKey, mediaType string) (string, error)
	PresignUploadPart(ctx context.Context, stagingKey, uploadID string, partNumber int, ttl time.Duration) (PresignedPart, error)
	CompleteMultipart(ctx context.Context, stagingKey, uploadID string, parts []CompletedPart) error
	AbortMultipart(ctx context.Context, stagingKey, uploadID string) error
	VerifyObject(ctx context.Context, key string, expected Descriptor) error
	PromoteToCAS(ctx context.Context, stagingKey string, expected Descriptor) error
	DeleteObject(ctx context.Context, key string) error
}

func MultipartPartSize(size, requested int64) (int64, int, error) {
	if size < 0 {
		return 0, 0, fmt.Errorf("artifact size must not be negative")
	}
	partSize := requested
	if partSize == 0 {
		partSize = DefaultMultipartPartSize
	}
	if partSize < MinMultipartPartSize {
		partSize = MinMultipartPartSize
	}
	if partSize > MaxMultipartPartSize {
		return 0, 0, fmt.Errorf("multipart part size exceeds %d bytes", MaxMultipartPartSize)
	}
	if size > 0 {
		minForPartLimit := (size + MaxMultipartParts - 1) / MaxMultipartParts
		if minForPartLimit > partSize {
			partSize = minForPartLimit
			// S3 allows arbitrary byte sizes within bounds; round upward to MiB
			// so clients have predictable part boundaries.
			const mib = int64(1 << 20)
			partSize = ((partSize + mib - 1) / mib) * mib
		}
	}
	if partSize > MaxMultipartPartSize {
		return 0, 0, fmt.Errorf("artifact is too large for S3 multipart limits")
	}
	partCount := 1
	if size > 0 {
		partCount = int((size + partSize - 1) / partSize)
	}
	if partCount > MaxMultipartParts {
		return 0, 0, fmt.Errorf("multipart upload requires %d parts; maximum is %d", partCount, MaxMultipartParts)
	}
	return partSize, partCount, nil
}

func (m *Manager) directMultipartStore() (DirectMultipartStore, error) {
	store, ok := m.store.(DirectMultipartStore)
	if !ok {
		return nil, ErrDirectUploadUnsupported
	}
	return store, nil
}

func (m *Manager) SupportsDirectMultipart() bool {
	_, ok := m.store.(DirectMultipartStore)
	return ok
}

func (m *Manager) BeginMultipart(ctx context.Context, stagingKey, mediaType string) (string, error) {
	store, err := m.directMultipartStore()
	if err != nil {
		return "", err
	}
	return store.BeginMultipart(ctx, stagingKey, mediaType)
}

func (m *Manager) PresignUploadParts(ctx context.Context, stagingKey, uploadID string, start, count int, ttl time.Duration) ([]PresignedPart, error) {
	store, err := m.directMultipartStore()
	if err != nil {
		return nil, err
	}
	if start < 1 || count < 1 {
		return nil, fmt.Errorf("part start and count must be positive")
	}
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	parts := make([]PresignedPart, 0, count)
	for number := start; number < start+count; number++ {
		part, err := store.PresignUploadPart(ctx, stagingKey, uploadID, number, ttl)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func (m *Manager) CompleteMultipart(ctx context.Context, stagingKey, uploadID string, parts []CompletedPart) error {
	store, err := m.directMultipartStore()
	if err != nil {
		return err
	}
	return store.CompleteMultipart(ctx, stagingKey, uploadID, parts)
}

func (m *Manager) AbortMultipart(ctx context.Context, stagingKey, uploadID string) error {
	store, err := m.directMultipartStore()
	if err != nil {
		return err
	}
	return store.AbortMultipart(ctx, stagingKey, uploadID)
}

func (m *Manager) DeleteDirectStaging(ctx context.Context, stagingKey string) error {
	store, err := m.directMultipartStore()
	if err != nil {
		return err
	}
	return store.DeleteObject(ctx, stagingKey)
}

func (m *Manager) VerifyAndPromoteMultipart(ctx context.Context, stagingKey string, expected Descriptor) error {
	store, err := m.directMultipartStore()
	if err != nil {
		return err
	}
	if err := store.VerifyObject(ctx, stagingKey, expected); err != nil {
		return err
	}
	if err := store.PromoteToCAS(ctx, stagingKey, expected); err != nil {
		return err
	}
	return store.DeleteObject(ctx, stagingKey)
}
