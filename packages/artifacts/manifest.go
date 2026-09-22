package artifacts

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

func NormalizeArtifactPath(value string) (string, error) {
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("artifact path contains NUL")
	}
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("artifact path must be relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("artifact path escapes repository root")
	}
	return clean, nil
}

func (a Artifact) Validate() error {
	normalized, err := NormalizeArtifactPath(a.Path)
	if err != nil {
		return err
	}
	if normalized != a.Path {
		return fmt.Errorf("artifact path %q is not canonical; use %q", a.Path, normalized)
	}
	if !a.Kind.Valid() {
		return fmt.Errorf("invalid artifact kind %q", a.Kind)
	}
	if !a.Descriptor.Digest.Valid() {
		return fmt.Errorf("artifact %q has invalid digest", a.Path)
	}
	if a.Descriptor.Size < 0 {
		return fmt.Errorf("artifact %q has negative size", a.Path)
	}
	if a.SemanticDigest != nil && !a.SemanticDigest.Valid() {
		return fmt.Errorf("artifact %q has invalid semantic digest", a.Path)
	}
	lastEnd := int64(0)
	for i, chunk := range a.Chunks {
		if !chunk.Descriptor.Digest.Valid() || chunk.Descriptor.Size < 0 || chunk.Offset < 0 {
			return fmt.Errorf("artifact %q has invalid chunk %d", a.Path, i)
		}
		if i > 0 && chunk.Offset < lastEnd {
			return fmt.Errorf("artifact %q has overlapping chunks", a.Path)
		}
		lastEnd = chunk.Offset + chunk.Descriptor.Size
	}
	for i, parent := range a.Parents {
		if !parent.Valid() {
			return fmt.Errorf("artifact %q has invalid parent %d", a.Path, i)
		}
	}
	for i, derivative := range a.Derivatives {
		if strings.TrimSpace(derivative.Role) == "" || !derivative.Descriptor.Digest.Valid() || derivative.Descriptor.Size < 0 {
			return fmt.Errorf("artifact %q has invalid derivative %d", a.Path, i)
		}
	}
	return nil
}

func NewManifest(artifacts []Artifact) (Manifest, error) {
	normalized := append([]Artifact(nil), artifacts...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Path < normalized[j].Path })

	for i := range normalized {
		if err := normalized[i].Validate(); err != nil {
			return Manifest{}, err
		}
		if i > 0 && normalized[i-1].Path == normalized[i].Path {
			return Manifest{}, fmt.Errorf("duplicate artifact path %q", normalized[i].Path)
		}
	}
	return Manifest{Schema: ManifestSchemaV1, Artifacts: normalized}, nil
}

func (m Manifest) CanonicalJSON() ([]byte, error) {
	if m.Schema != ManifestSchemaV1 {
		return nil, fmt.Errorf("unsupported artifact manifest schema %q", m.Schema)
	}
	normalized, err := NewManifest(m.Artifacts)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func (m Manifest) Digest() (Digest, error) {
	payload, err := m.CanonicalJSON()
	if err != nil {
		return Digest{}, err
	}
	return HashBytes(payload), nil
}

func DecodeManifest(payload []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode artifact manifest: %w", err)
	}
	canonical, err := NewManifest(manifest.Artifacts)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Schema != ManifestSchemaV1 {
		return Manifest{}, fmt.Errorf("unsupported artifact manifest schema %q", manifest.Schema)
	}
	return canonical, nil
}

func (v Version) CanonicalJSON() ([]byte, error) {
	if v.Schema != VersionSchemaV1 {
		return nil, fmt.Errorf("unsupported artifact version schema %q", v.Schema)
	}
	if !v.Manifest.Valid() {
		return nil, fmt.Errorf("artifact version has invalid manifest digest")
	}
	if v.CreatedAt.IsZero() {
		return nil, fmt.Errorf("artifact version created_at is required")
	}
	for i, parent := range v.Parents {
		if !parent.Valid() {
			return nil, fmt.Errorf("artifact version has invalid parent %d", i)
		}
	}
	return json.Marshal(v)
}

func (v Version) Digest() (Digest, error) {
	payload, err := v.CanonicalJSON()
	if err != nil {
		return Digest{}, err
	}
	return HashBytes(payload), nil
}
