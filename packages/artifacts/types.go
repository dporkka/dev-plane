package artifacts

import "time"

const (
	ManifestSchemaV1 = "devplane.artifact-manifest.v1"
	VersionSchemaV1  = "devplane.artifact-version.v1"
)

type Kind string

const (
	KindText         Kind = "text"
	KindDocument     Kind = "document"
	KindSpreadsheet  Kind = "spreadsheet"
	KindPresentation Kind = "presentation"
	KindPDF          Kind = "pdf"
	KindImage        Kind = "image"
	KindAudio        Kind = "audio"
	KindVideo        Kind = "video"
	KindThreeD       Kind = "3d"
	KindDataset      Kind = "dataset"
	KindBinary       Kind = "binary"
)

func (k Kind) Valid() bool {
	switch k {
	case KindText, KindDocument, KindSpreadsheet, KindPresentation, KindPDF,
		KindImage, KindAudio, KindVideo, KindThreeD, KindDataset, KindBinary:
		return true
	default:
		return false
	}
}

// Descriptor identifies one immutable payload in content-addressed storage.
type Descriptor struct {
	Digest    Digest `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
}

// ChunkRef describes one independently-addressed range of a large artifact.
// Chunk production is intentionally outside the core package so FastCDC or
// another chunker can be added without changing the manifest schema.
type ChunkRef struct {
	Descriptor Descriptor `json:"descriptor"`
	Offset     int64      `json:"offset"`
}

type GeneratorInfo struct {
	Name       string            `json:"name"`
	Version    string            `json:"version,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

type DerivativeRef struct {
	Role       string         `json:"role"`
	Descriptor Descriptor     `json:"descriptor"`
	Generator  *GeneratorInfo `json:"generator,omitempty"`
}

// Artifact is the logical file entry exposed to users and agents. Descriptor
// points at the byte-exact original. Semantic and derivative representations
// are optional and never replace the original payload.
type Artifact struct {
	Path           string            `json:"path"`
	Kind           Kind              `json:"kind"`
	Descriptor     Descriptor        `json:"descriptor"`
	SemanticDigest *Digest           `json:"semantic_digest,omitempty"`
	Chunks         []ChunkRef        `json:"chunks,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Parents        []Digest          `json:"parents,omitempty"`
	Derivatives    []DerivativeRef   `json:"derivatives,omitempty"`
}

type Manifest struct {
	Schema    string     `json:"schema"`
	Artifacts []Artifact `json:"artifacts"`
}

type ActorIdentity struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

type Provenance struct {
	TaskID         string `json:"task_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
}

// Version is the immutable commit-like object for an artifact tree. Parents
// reference prior version objects and Manifest references the immutable
// manifest payload in the CAS.
type Version struct {
	Schema     string        `json:"schema"`
	Parents    []Digest      `json:"parents,omitempty"`
	Manifest   Digest        `json:"manifest"`
	Author     ActorIdentity `json:"author,omitempty"`
	Message    string        `json:"message,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	Provenance Provenance    `json:"provenance,omitempty"`
}
