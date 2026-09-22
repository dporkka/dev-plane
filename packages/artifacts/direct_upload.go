package artifacts

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrDirectUploadUnsupported = errors.New("direct multipart upload is not supported by this artifact store")

const MultipartChecksumCRC64NVME = "CRC64NVME"

type MultipartChecksum struct {
	Algorithm string `json:"algorithm"`
	Base64    string `json:"base64"`
}

func ParseMultipartChecksum(algorithm, value string) (*MultipartChecksum, error) {
	algorithm = strings.ToUpper(strings.TrimSpace(algorithm))
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if algorithm != MultipartChecksumCRC64NVME {
		return nil, fmt.Errorf("unsupported multipart checksum algorithm %q", algorithm)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s checksum: %w", algorithm, err)
	}
	if len(decoded) != 8 {
		return nil, fmt.Errorf("%s checksum must decode to 8 bytes", algorithm)
	}
	return &MultipartChecksum{Algorithm: algorithm, Base64: value}, nil
}

const (
	MinMultipartPartSize     int64 = 5 << 20
	DefaultMultipartPartSize int64 = 64 << 20
	MaxMultipartPartSize     int64 = 5 << 30
	MaxMultipartParts              = 10000
	DefaultPresignTTL              = 15 * time.Minute
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

type DirectMultipartChecksumStore interface {
	BeginMultipartWithChecksum(
		ctx context.Context,
		stagingKey, mediaType string,
		checksum *MultipartChecksum,
	) (uploadID string, nativeChecksumEnabled bool, err error)
	CompleteMultipartWithChecksum(
		ctx context.Context,
		stagingKey, uploadID string,
		parts []CompletedPart,
		expected Descriptor,
		checksum *MultipartChecksum,
	) error
	VerifyObjectChecksum(
		ctx context.Context,
		key string,
		expected Descriptor,
		checksum *MultipartChecksum,
	) (verified bool, err error)
}

type DirectMultipartSHA256Store interface {
	VerifyAndPromoteSHA256(
		ctx context.Context,
		stagingKey string,
		expected Descriptor,
	) (verified bool, mode string, err error)
}

func MultipartPartSize(size, requested int64) (int64, int, error) {
	if size <= 0 {
		return 0, 0, fmt.Errorf("artifact size must be positive for multipart upload")
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
	partCount := int((size + partSize - 1) / partSize)
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

func (m *Manager) BeginMultipartWithChecksum(
	ctx context.Context,
	stagingKey, mediaType string,
	checksum *MultipartChecksum,
) (string, bool, error) {
	store, err := m.directMultipartStore()
	if err != nil {
		return "", false, err
	}
	if enhanced, ok := store.(DirectMultipartChecksumStore); ok && checksum != nil {
		return enhanced.BeginMultipartWithChecksum(ctx, stagingKey, mediaType, checksum)
	}
	uploadID, err := store.BeginMultipart(ctx, stagingKey, mediaType)
	return uploadID, false, err
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

func (m *Manager) CompleteMultipartWithChecksum(
	ctx context.Context,
	stagingKey, uploadID string,
	parts []CompletedPart,
	expected Descriptor,
	checksum *MultipartChecksum,
	nativeChecksumEnabled bool,
) error {
	store, err := m.directMultipartStore()
	if err != nil {
		return err
	}
	if nativeChecksumEnabled && checksum != nil {
		if enhanced, ok := store.(DirectMultipartChecksumStore); ok {
			return enhanced.CompleteMultipartWithChecksum(ctx, stagingKey, uploadID, parts, expected, checksum)
		}
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
	_, err := m.VerifyAndPromoteMultipartWithChecksum(ctx, stagingKey, expected, nil, false)
	return err
}

func (m *Manager) VerifyAndPromoteMultipartWithChecksum(
	ctx context.Context,
	stagingKey string,
	expected Descriptor,
	checksum *MultipartChecksum,
	nativeChecksumEnabled bool,
) (string, error) {
	store, err := m.directMultipartStore()
	if err != nil {
		return "", err
	}

	// CAS identity is SHA-256. If this digest already exists, the canonical
	// bytes are already trusted and the staged duplicate can be discarded.
	exists, err := m.store.Has(ctx, expected.Digest)
	if err != nil {
		return "", err
	}
	if exists {
		if err := store.DeleteObject(ctx, stagingKey); err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
		return "cas_existing", nil
	}

	// CRC64/NVME is useful for provider-side transport validation, but it does
	// not prove the client-supplied SHA-256 CAS identity. Treat it as an
	// additional check only; a full SHA-256 proof is still required below.
	if nativeChecksumEnabled && checksum != nil {
		if enhanced, ok := store.(DirectMultipartChecksumStore); ok {
			if _, err := enhanced.VerifyObjectChecksum(ctx, stagingKey, expected, checksum); err != nil {
				return "", err
			}
		}
	}

	// Some stores can compute a direct full-object SHA-256 without sending the
	// bytes through Dev Plane. This method must only return verified=true after
	// comparing a provider-computed SHA-256 to expected.Digest and promoting
	// those exact bytes into CAS.
	if enhanced, ok := store.(DirectMultipartSHA256Store); ok {
		verified, mode, err := enhanced.VerifyAndPromoteSHA256(ctx, stagingKey, expected)
		if err != nil {
			return "", err
		}
		if verified {
			return mode, nil
		}
	}

	if err := store.VerifyObject(ctx, stagingKey, expected); err != nil {
		return "", err
	}
	if err := store.PromoteToCAS(ctx, stagingKey, expected); err != nil {
		return "", err
	}
	if err := store.DeleteObject(ctx, stagingKey); err != nil {
		return "", err
	}
	return "stream_sha256", nil
}
