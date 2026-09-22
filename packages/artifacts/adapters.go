package artifacts

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	SemanticDocumentMediaType = "application/vnd.dev-plane.document-semantics.v1+json"
	SemanticImageMediaType    = "application/vnd.dev-plane.image-semantics.v1+json"
)

type DetectedFormat struct {
	Kind      Kind   `json:"kind"`
	MediaType string `json:"media_type"`
	Extension string `json:"extension,omitempty"`
}

type AnalysisInput struct {
	Path     string
	Format   DetectedFormat
	File     *os.File
	Size     int64
	MaxBytes int64
}

type GeneratedPayload struct {
	Role      string
	MediaType string
	Data      []byte
}

type AdapterAnalysis struct {
	Adapter     string
	Version     string
	Metadata    map[string]string
	Semantic    *GeneratedPayload
	Derivatives []GeneratedPayload
	Warnings    []string
}

type ArtifactAdapter interface {
	Name() string
	Version() string
	Supports(format DetectedFormat) bool
	Analyze(ctx context.Context, input AnalysisInput) (AdapterAnalysis, error)
}

type AdapterRegistry struct {
	adapters []ArtifactAdapter
}

func NewAdapterRegistry(adapters ...ArtifactAdapter) *AdapterRegistry {
	registry := &AdapterRegistry{}
	for _, adapter := range adapters {
		if adapter != nil {
			registry.adapters = append(registry.adapters, adapter)
		}
	}
	return registry
}

func NewDefaultAdapterRegistry() *AdapterRegistry {
	return NewAdapterRegistry(
		NewDOCXAdapter(),
		NewXLSXAdapter(),
		NewPPTXAdapter(),
		NewImageAdapter(1024),
		NewPDFAdapter(PopplerRunner{}),
	)
}

func (r *AdapterRegistry) Register(adapter ArtifactAdapter) {
	if r == nil || adapter == nil {
		return
	}
	r.adapters = append(r.adapters, adapter)
}

func (r *AdapterRegistry) Find(format DetectedFormat) ArtifactAdapter {
	if r == nil {
		return nil
	}
	for _, adapter := range r.adapters {
		if adapter.Supports(format) {
			return adapter
		}
	}
	return nil
}

func (r *AdapterRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		names = append(names, adapter.Name())
	}
	sort.Strings(names)
	return names
}

type AnalyzeOptions struct {
	TempDir  string
	MaxBytes int64
}

func (m *Manager) AnalyzeArtifact(ctx context.Context, registry *AdapterRegistry, artifact Artifact, options AnalyzeOptions) (Artifact, AdapterAnalysis, error) {
	if registry == nil {
		return Artifact{}, AdapterAnalysis{}, fmt.Errorf("artifact adapter registry is required")
	}
	if err := artifact.Validate(); err != nil {
		return Artifact{}, AdapterAnalysis{}, err
	}

	tmp, err := os.CreateTemp(options.TempDir, "dev-plane-analyze-*")
	if err != nil {
		return Artifact{}, AdapterAnalysis{}, fmt.Errorf("create artifact analysis temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()

	if err := m.Materialize(ctx, artifact, tmp); err != nil {
		return Artifact{}, AdapterAnalysis{}, err
	}
	info, err := tmp.Stat()
	if err != nil {
		return Artifact{}, AdapterAnalysis{}, fmt.Errorf("stat artifact analysis input: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return Artifact{}, AdapterAnalysis{}, fmt.Errorf("rewind artifact analysis input: %w", err)
	}

	format, err := DetectFormat(artifact.Path, tmp, info.Size())
	if err != nil {
		return Artifact{}, AdapterAnalysis{}, err
	}
	adapter := registry.Find(format)
	if adapter == nil {
		updated := artifact
		if updated.Descriptor.MediaType == "" {
			updated.Descriptor.MediaType = format.MediaType
		}
		if updated.Kind == KindBinary && format.Kind.Valid() {
			updated.Kind = format.Kind
		}
		return updated, AdapterAnalysis{
			Metadata: map[string]string{
				"detected_media_type": format.MediaType,
				"analysis_status":      "no_adapter",
			},
		}, nil
	}

	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return Artifact{}, AdapterAnalysis{}, fmt.Errorf("rewind artifact for adapter: %w", err)
	}
	analysis, err := adapter.Analyze(ctx, AnalysisInput{
		Path: artifact.Path, Format: format, File: tmp, Size: info.Size(), MaxBytes: maxBytes,
	})
	if err != nil {
		return Artifact{}, AdapterAnalysis{}, fmt.Errorf("analyze artifact with %s: %w", adapter.Name(), err)
	}
	if analysis.Adapter == "" {
		analysis.Adapter = adapter.Name()
	}
	if analysis.Version == "" {
		analysis.Version = adapter.Version()
	}

	updated := artifact
	updated.Metadata = cloneStringMap(artifact.Metadata)
	if updated.Descriptor.MediaType == "" || updated.Descriptor.MediaType == "application/octet-stream" {
		updated.Descriptor.MediaType = format.MediaType
	}
	if updated.Kind == KindBinary && format.Kind.Valid() {
		updated.Kind = format.Kind
	}
	if updated.Metadata == nil {
		updated.Metadata = map[string]string{}
	}
	updated.Metadata["detected_media_type"] = format.MediaType
	updated.Metadata["artifact_adapter"] = analysis.Adapter
	updated.Metadata["artifact_adapter_version"] = analysis.Version
	for key, value := range analysis.Metadata {
		updated.Metadata[key] = value
	}

	generator := &GeneratorInfo{Name: analysis.Adapter, Version: analysis.Version}
	if analysis.Semantic != nil && len(analysis.Semantic.Data) > 0 {
		descriptor, err := m.store.Put(ctx, bytes.NewReader(analysis.Semantic.Data))
		if err != nil {
			return Artifact{}, AdapterAnalysis{}, fmt.Errorf("store semantic representation: %w", err)
		}
		descriptor.MediaType = analysis.Semantic.MediaType
		digest := descriptor.Digest
		updated.SemanticDigest = &digest
		updated.Derivatives = upsertDerivative(updated.Derivatives, DerivativeRef{
			Role:       semanticRole(analysis.Semantic.Role),
			Descriptor: descriptor,
			Generator:  generator,
		})
	}

	for _, generated := range analysis.Derivatives {
		if generated.Role == "" || len(generated.Data) == 0 {
			continue
		}
		descriptor, err := m.store.Put(ctx, bytes.NewReader(generated.Data))
		if err != nil {
			return Artifact{}, AdapterAnalysis{}, fmt.Errorf("store derivative %q: %w", generated.Role, err)
		}
		descriptor.MediaType = generated.MediaType
		updated.Derivatives = upsertDerivative(updated.Derivatives, DerivativeRef{
			Role:       generated.Role,
			Descriptor: descriptor,
			Generator:  generator,
		})
	}

	if err := updated.Validate(); err != nil {
		return Artifact{}, AdapterAnalysis{}, err
	}
	return updated, analysis, nil
}

func semanticRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "semantic"
	}
	return role
}

func upsertDerivative(existing []DerivativeRef, replacement DerivativeRef) []DerivativeRef {
	out := make([]DerivativeRef, 0, len(existing)+1)
	replaced := false
	for _, item := range existing {
		if item.Role == replacement.Role {
			if !replaced {
				out = append(out, replacement)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}
