package models

import (
	"encoding/json"
	"errors"
	"time"
)

// WorkspaceSnapshot binds one source-control snapshot to the optional artifact
// manifest/version representing non-code files at the same point in time.
type WorkspaceSnapshot struct {
	ID                     string          `json:"id"`
	WorkspaceID            string          `json:"workspace_id"`
	AgentRunID             *string         `json:"agent_run_id,omitempty"`
	GitCommit              *string         `json:"git_commit,omitempty"`
	VCSChangeID            *string         `json:"vcs_change_id,omitempty"`
	ArtifactManifestDigest *string         `json:"artifact_manifest_digest,omitempty"`
	ArtifactVersionDigest  *string         `json:"artifact_version_digest,omitempty"`
	Description            *string         `json:"description,omitempty"`
	Metadata               json.RawMessage `json:"metadata,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
}

func (s *WorkspaceSnapshot) Validate() error {
	if s.WorkspaceID == "" {
		return errors.New("workspace snapshot workspace_id is required")
	}
	if s.GitCommit == nil && s.ArtifactManifestDigest == nil && s.ArtifactVersionDigest == nil {
		return errors.New("workspace snapshot requires source-control or artifact state")
	}
	return nil
}
