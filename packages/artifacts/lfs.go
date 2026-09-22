package artifacts

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

const LFSVersionURL = "https://git-lfs.github.com/spec/v1"

// LFSPointer is the interoperable Git LFS pointer representation for one
// artifact payload. Dev Plane's CAS remains canonical; this type is an import /
// export bridge for normal Git tooling.
type LFSPointer struct {
	Digest Digest
	Size   int64
}

func NewLFSPointer(descriptor Descriptor) (LFSPointer, error) {
	if !descriptor.Digest.Valid() || descriptor.Digest.Algorithm != AlgorithmSHA256 {
		return LFSPointer{}, fmt.Errorf("Git LFS requires a valid SHA-256 digest")
	}
	if descriptor.Size < 0 {
		return LFSPointer{}, fmt.Errorf("Git LFS size must be non-negative")
	}
	return LFSPointer{Digest: descriptor.Digest, Size: descriptor.Size}, nil
}

func (p LFSPointer) MarshalText() ([]byte, error) {
	if !p.Digest.Valid() || p.Digest.Algorithm != AlgorithmSHA256 {
		return nil, fmt.Errorf("Git LFS requires a valid SHA-256 digest")
	}
	if p.Size < 0 {
		return nil, fmt.Errorf("Git LFS size must be non-negative")
	}
	return []byte(fmt.Sprintf(
		"version %s\noid %s\nsize %d\n",
		LFSVersionURL,
		p.Digest.String(),
		p.Size,
	)), nil
}

func ParseLFSPointer(payload []byte) (LFSPointer, error) {
	var (
		versionSeen bool
		digest      Digest
		size        int64
		sizeSeen    bool
	)

	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "version "):
			if strings.TrimSpace(strings.TrimPrefix(line, "version ")) != LFSVersionURL {
				return LFSPointer{}, fmt.Errorf("unsupported Git LFS pointer version")
			}
			versionSeen = true
		case strings.HasPrefix(line, "oid "):
			parsed, err := ParseDigest(strings.TrimSpace(strings.TrimPrefix(line, "oid ")))
			if err != nil {
				return LFSPointer{}, err
			}
			if parsed.Algorithm != AlgorithmSHA256 {
				return LFSPointer{}, fmt.Errorf("Git LFS pointer must use SHA-256")
			}
			digest = parsed
		case strings.HasPrefix(line, "size "):
			parsed, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "size ")), 10, 64)
			if err != nil || parsed < 0 {
				return LFSPointer{}, fmt.Errorf("invalid Git LFS pointer size")
			}
			size = parsed
			sizeSeen = true
		case strings.HasPrefix(line, "ext-"):
			// Git LFS extension lines are allowed but are not needed to locate
			// the underlying immutable object.
			continue
		default:
			return LFSPointer{}, fmt.Errorf("unsupported Git LFS pointer line %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return LFSPointer{}, fmt.Errorf("read Git LFS pointer: %w", err)
	}
	if !versionSeen || !digest.Valid() || !sizeSeen {
		return LFSPointer{}, fmt.Errorf("incomplete Git LFS pointer")
	}
	return LFSPointer{Digest: digest, Size: size}, nil
}
