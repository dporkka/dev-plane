package reviewer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
)

type ArtifactChange struct {
	Path        string                 `json:"path"`
	Status      string                 `json:"status"`
	Kind        artifactstore.Kind     `json:"kind"`
	MediaType   string                 `json:"media_type,omitempty"`
	Semantic    bool                   `json:"semantic"`
	ChangeCount int                    `json:"change_count,omitempty"`
	Changes     []artifactstore.SequenceChange `json:"changes,omitempty"`
	Warnings    []string               `json:"warnings,omitempty"`
}

type artifactDiffSummary struct {
	FilesChanged  int
	Additions     int
	Modifications int
	Deletions     int
	Changes       []ArtifactChange
}

func (r *Reviewer) getArtifactDiffSummary(ctx context.Context, runID, workspaceID string) (artifactDiffSummary, error) {
	if r.artifactManager == nil {
		return artifactDiffSummary{}, nil
	}

	var (
		currentVersionRaw sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT artifact_version_digest
		FROM workspace_snapshots
		WHERE agent_run_id = $1 AND workspace_id = $2
		LIMIT 1
	`, runID, workspaceID).Scan(&currentVersionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return artifactDiffSummary{}, nil
	}
	if err != nil {
		return artifactDiffSummary{}, fmt.Errorf("load candidate artifact snapshot: %w", err)
	}
	if !currentVersionRaw.Valid {
		return artifactDiffSummary{}, nil
	}

	currentManifest, err := r.loadVersionManifest(ctx, currentVersionRaw.String)
	if err != nil {
		return artifactDiffSummary{}, fmt.Errorf("load current artifact manifest: %w", err)
	}

	// Review cumulative artifact state for the task/workspace rather than only
	// the last handoff. The baseline is the latest non-agent snapshot that
	// predates the first candidate; a fresh workspace naturally starts empty.
	previousManifest := artifactstore.Manifest{Schema: artifactstore.ManifestSchemaV1}
	var firstCandidateAt sql.NullTime
	err = r.db.QueryRowContext(ctx, `
		SELECT MIN(created_at)
		FROM workspace_snapshots
		WHERE workspace_id = $1 AND agent_run_id IS NOT NULL
	`, workspaceID).Scan(&firstCandidateAt)
	if err != nil {
		return artifactDiffSummary{}, fmt.Errorf("load first candidate timestamp: %w", err)
	}
	if firstCandidateAt.Valid {
		var baselineVersionRaw sql.NullString
		err = r.db.QueryRowContext(ctx, `
			SELECT artifact_version_digest
			FROM workspace_snapshots
			WHERE workspace_id = $1
			  AND agent_run_id IS NULL
			  AND artifact_version_digest IS NOT NULL
			  AND created_at < $2
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		`, workspaceID, firstCandidateAt.Time).Scan(&baselineVersionRaw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return artifactDiffSummary{}, fmt.Errorf("load artifact baseline snapshot: %w", err)
		}
		if baselineVersionRaw.Valid {
			previousManifest, err = r.loadVersionManifest(ctx, baselineVersionRaw.String)
			if err != nil {
				return artifactDiffSummary{}, fmt.Errorf("load artifact baseline manifest: %w", err)
			}
		}
	}

	before := make(map[string]artifactstore.Artifact, len(previousManifest.Artifacts))
	for _, artifact := range previousManifest.Artifacts {
		before[artifact.Path] = artifact
	}
	after := make(map[string]artifactstore.Artifact, len(currentManifest.Artifacts))
	for _, artifact := range currentManifest.Artifacts {
		after[artifact.Path] = artifact
	}

	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}

	result := artifactDiffSummary{}
	for path := range paths {
		left, hadBefore := before[path]
		right, hasAfter := after[path]
		switch {
		case !hadBefore && hasAfter:
			result.Additions++
			result.Changes = append(result.Changes, ArtifactChange{
				Path: path, Status: "added", Kind: right.Kind, MediaType: right.Descriptor.MediaType,
			})
		case hadBefore && !hasAfter:
			result.Deletions++
			result.Changes = append(result.Changes, ArtifactChange{
				Path: path, Status: "deleted", Kind: left.Kind, MediaType: left.Descriptor.MediaType,
			})
		case hadBefore && hasAfter && left.Descriptor.Digest != right.Descriptor.Digest:
			diff, err := r.artifactManager.DiffArtifacts(ctx, left, right)
			if err != nil {
				return artifactDiffSummary{}, fmt.Errorf("diff artifact %q: %w", path, err)
			}
			result.Modifications++
			result.Changes = append(result.Changes, ArtifactChange{
				Path:        path,
				Status:      "modified",
				Kind:        right.Kind,
				MediaType:   right.Descriptor.MediaType,
				Semantic:    diff.Semantic,
				ChangeCount: len(diff.Changes) + len(diff.MetadataChanges),
				Changes:     diff.Changes,
				Warnings:    diff.Warnings,
			})
		}
	}
	sort.Slice(result.Changes, func(i, j int) bool { return result.Changes[i].Path < result.Changes[j].Path })
	result.FilesChanged = len(result.Changes)
	return result, nil
}

func (r *Reviewer) loadVersionManifest(ctx context.Context, raw string) (artifactstore.Manifest, error) {
	digest, err := artifactstore.ParseDigest(raw)
	if err != nil {
		return artifactstore.Manifest{}, err
	}
	version, err := r.artifactManager.LoadVersion(ctx, digest)
	if err != nil {
		return artifactstore.Manifest{}, err
	}
	return r.artifactManager.LoadManifest(ctx, version.Manifest)
}
