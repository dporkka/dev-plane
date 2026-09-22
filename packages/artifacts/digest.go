package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const AlgorithmSHA256 = "sha256"

type Digest struct {
	Algorithm string `json:"algorithm"`
	Hex       string `json:"hex"`
}

func (d Digest) String() string {
	if d.Algorithm == "" && d.Hex == "" {
		return ""
	}
	return d.Algorithm + ":" + d.Hex
}

func (d Digest) Valid() bool {
	if d.Algorithm != AlgorithmSHA256 || len(d.Hex) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(d.Hex)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(d.Hex) == d.Hex
}

func ParseDigest(value string) (Digest, error) {
	algorithm, encoded, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return Digest{}, fmt.Errorf("artifact digest must use algorithm:hex form")
	}
	digest := Digest{Algorithm: algorithm, Hex: strings.ToLower(encoded)}
	if !digest.Valid() {
		return Digest{}, fmt.Errorf("invalid artifact digest %q", value)
	}
	return digest, nil
}

func HashBytes(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest{Algorithm: AlgorithmSHA256, Hex: hex.EncodeToString(sum[:])}
}

func HashReader(r io.Reader) (Digest, int64, error) {
	if r == nil {
		return Digest{}, 0, fmt.Errorf("artifact reader is required")
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, r)
	if err != nil {
		return Digest{}, size, fmt.Errorf("hash artifact: %w", err)
	}
	return Digest{Algorithm: AlgorithmSHA256, Hex: hex.EncodeToString(hasher.Sum(nil))}, size, nil
}
