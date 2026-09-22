package artifacts

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

type ChunkingConfig struct {
	MinSize     int `json:"min_size"`
	AverageSize int `json:"average_size"`
	MaxSize     int `json:"max_size"`
}

func DefaultChunkingConfig() ChunkingConfig {
	return ChunkingConfig{
		MinSize:     512 << 10,
		AverageSize: 2 << 20,
		MaxSize:     8 << 20,
	}
}

func (c ChunkingConfig) Validate() error {
	if c.MinSize <= 0 || c.AverageSize <= 0 || c.MaxSize <= 0 {
		return fmt.Errorf("chunk sizes must be positive")
	}
	if c.MinSize > c.AverageSize || c.AverageSize > c.MaxSize {
		return fmt.Errorf("chunk sizes must satisfy min <= average <= max")
	}
	if c.AverageSize&(c.AverageSize-1) != 0 {
		return fmt.Errorf("average chunk size must be a power of two")
	}
	return nil
}

// ContentDefinedChunker implements a FastCDC-inspired gear-hash splitter.
//
// Boundaries depend on payload content instead of absolute byte offsets. Small
// insertions/deletions therefore disturb only nearby chunks and the stream
// quickly resynchronizes, allowing unchanged chunks to be reused from the CAS.
type ContentDefinedChunker struct {
	config ChunkingConfig
	mask   uint64
}

func NewContentDefinedChunker(config ChunkingConfig) (*ContentDefinedChunker, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &ContentDefinedChunker{
		config: config,
		mask:   uint64(config.AverageSize - 1),
	}, nil
}

// Put splits r into independently-addressed CAS blobs while also computing the
// SHA-256 identity of the byte-exact full artifact. The complete object need
// not exist as a single CAS blob; Materialize reconstructs it from Chunks.
func (c *ContentDefinedChunker) Put(ctx context.Context, store BlobStore, r io.Reader) (Descriptor, []ChunkRef, error) {
	if store == nil {
		return Descriptor{}, nil, fmt.Errorf("artifact blob store is required")
	}
	if r == nil {
		return Descriptor{}, nil, fmt.Errorf("artifact reader is required")
	}

	reader := bufio.NewReaderSize(r, 128<<10)
	fullHash := sha256.New()
	var (
		chunk      bytes.Buffer
		chunkHash  uint64
		offset     int64
		total      int64
		chunks     []ChunkRef
		scratch    = make([]byte, 128<<10)
	)

	flush := func() error {
		descriptor, err := store.Put(ctx, bytes.NewReader(chunk.Bytes()))
		if err != nil {
			return err
		}
		chunks = append(chunks, ChunkRef{
			Descriptor: descriptor,
			Offset:     offset,
		})
		offset += descriptor.Size
		chunk.Reset()
		chunkHash = 0
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return Descriptor{}, nil, err
		}
		n, err := reader.Read(scratch)
		if n > 0 {
			part := scratch[:n]
			_, _ = fullHash.Write(part)
			total += int64(n)
			for _, b := range part {
				_ = chunk.WriteByte(b)
				chunkHash = (chunkHash << 1) + gearTable[b]
				size := chunk.Len()
				if size >= c.config.MinSize &&
					((chunkHash&c.mask) == 0 || size >= c.config.MaxSize) {
					if err := flush(); err != nil {
						return Descriptor{}, nil, fmt.Errorf("store artifact chunk: %w", err)
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Descriptor{}, nil, fmt.Errorf("read chunked artifact: %w", err)
		}
	}

	// Keep the empty artifact representable as a chunked object and flush any
	// trailing bytes that did not hit a content boundary.
	if chunk.Len() > 0 || len(chunks) == 0 {
		if err := flush(); err != nil {
			return Descriptor{}, nil, fmt.Errorf("store final artifact chunk: %w", err)
		}
	}

	full := Descriptor{
		Digest: Digest{Algorithm: AlgorithmSHA256, Hex: hex.EncodeToString(fullHash.Sum(nil))},
		Size:   total,
	}
	return full, chunks, nil
}

var gearTable = func() [256]uint64 {
	// Stable SplitMix64 expansion. The generated table is deterministic across
	// processes/platforms but contains enough entropy for gear-hash boundaries.
	var table [256]uint64
	const seed uint64 = 0x6a09e667f3bcc909
	for i := range table {
		x := seed + uint64(i+1)*0x9e3779b97f4a7c15
		x ^= x >> 30
		x *= 0xbf58476d1ce4e5b9
		x ^= x >> 27
		x *= 0x94d049bb133111eb
		x ^= x >> 31
		table[i] = x
	}
	return table
}()
