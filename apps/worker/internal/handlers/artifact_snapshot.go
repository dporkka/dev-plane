package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/google/uuid"
)

// CandidateSnapshotter captures the immutable source + artifact state produced
// by a completed agent run. Implementations must be idempotent by run ID because
// completion events may be redelivered.
type CandidateSnapshotter interface {
	Capture(ctx context.Context, runID string) (*models.WorkspaceSnapshot, error)
}

type ArtifactSnapshotter struct {
	db      *sql.DB
	manager *artifactstore.Manager
	runtime runtimes.Provider
}

func NewArtifactSnapshotter(db *sql.DB, manager *artifactstore.Manager, runtimeProvider runtimes.Provider) *ArtifactSnapshotter {
	return &ArtifactSnapshotter{db: db, manager: manager, runtime: runtimeProvider}
}

func (s *ArtifactSnapshotter) Capture(ctx context.Context, runID string) (*models.WorkspaceSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("candidate snapshotter database is required")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("candidate snapshot run id is required")
	}
	if existing, err := s.loadRunSnapshot(ctx, runID); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var (
		taskID          string
		workspaceID     string
		agentRole       string
		runtimeSession  sql.NullString
		worktreePath    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT ar.task_id, ar.workspace_id, ar.agent_role,
		       w.runtime_session_id, w.worktree_path
		FROM agent_runs ar
		JOIN workspaces w ON w.id = ar.workspace_id
		WHERE ar.id = $1
	`, runID).Scan(&taskID, &workspaceID, &agentRole, &runtimeSession, &worktreePath)
	if err != nil {
		return nil, fmt.Errorf("load completed run workspace: %w", err)
	}

	sourceSnapshot, err := s.captureSource(ctx, workspaceID, runtimeSession, worktreePath)
	if err != nil {
		return nil, err
	}

	manifestDigest, versionDigest, err := s.captureArtifacts(
		ctx,
		runID,
		taskID,
		workspaceID,
		agentRole,
		sourceSnapshot.GitCommit,
	)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	id := uuid.NewString()
	description := "agent candidate snapshot"
	metadata, _ := json.Marshal(map[string]any{
		"trigger":    "agent_run_completed",
		"agent_role": agentRole,
	})
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO workspace_snapshots (
			id, workspace_id, agent_run_id, git_commit, vcs_change_id,
			artifact_manifest_digest, artifact_version_digest,
			description, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		id, workspaceID, runID,
		nullString(sourceSnapshot.GitCommit), nullString(sourceSnapshot.VCSChangeID),
		nullString(manifestDigest), nullString(versionDigest),
		description, string(metadata), now,
	)
	if err != nil {
		// A duplicate can only arise from a concurrent redelivery. Return the
		// already-persisted immutable snapshot rather than creating a sibling.
		if existing, loadErr := s.loadRunSnapshot(ctx, runID); loadErr == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("persist candidate snapshot: %w", err)
	}
	return &models.WorkspaceSnapshot{
		ID:                     id,
		WorkspaceID:            workspaceID,
		AgentRunID:             stringPtr(runID),
		GitCommit:              optionalString(sourceSnapshot.GitCommit),
		VCSChangeID:            optionalString(sourceSnapshot.VCSChangeID),
		ArtifactManifestDigest: optionalString(manifestDigest),
		ArtifactVersionDigest:  optionalString(versionDigest),
		Description:            stringPtr(description),
		Metadata:               metadata,
		CreatedAt:              now,
	}, nil
}

func (s *ArtifactSnapshotter) captureSource(
	ctx context.Context,
	workspaceID string,
	runtimeSession, worktreePath sql.NullString,
) (*runtimes.Snapshot, error) {
	if s.runtime != nil && runtimeSession.Valid && strings.TrimSpace(runtimeSession.String) != "" {
		snap, err := s.runtime.Snapshot(ctx, runtimeSession.String)
		if err != nil {
			return nil, fmt.Errorf("capture runtime source snapshot: %w", err)
		}
		return snap, nil
	}
	if !worktreePath.Valid || strings.TrimSpace(worktreePath.String) == "" {
		return nil, fmt.Errorf("workspace %s has no runtime session or worktree", workspaceID)
	}
	worktree := worktreePath.String
	if out, err := exec.CommandContext(ctx, "git", "-C", worktree, "add", "-A").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("snapshot git add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	cmd := exec.CommandContext(
		ctx, "git", "-C", worktree,
		"-c", "user.email=dev-plane@example.invalid",
		"-c", "user.name=Dev Plane",
		"commit", "--allow-empty", "-m", "snapshot: agent candidate",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("snapshot git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("snapshot git rev-parse: %w: %s", err, strings.TrimSpace(string(out)))
	}
	commit := strings.TrimSpace(string(out))
	return &runtimes.Snapshot{
		ID:          commit,
		SessionID:   workspaceID,
		GitCommit:   commit,
		Description: "Agent candidate source snapshot",
		CreatedAt:   time.Now().UTC(),
	}, nil
}

func (s *ArtifactSnapshotter) captureArtifacts(
	ctx context.Context,
	runID, taskID, workspaceID, agentRole, sourceRevision string,
) (string, string, error) {
	if s.manager == nil {
		return "", "", nil
	}
	tree, err := s.currentArtifactTree(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	items := make([]artifactstore.Artifact, 0, len(tree))
	for _, artifact := range tree {
		items = append(items, artifact)
	}
	manifest, err := artifactstore.NewManifest(items)
	if err != nil {
		return "", "", fmt.Errorf("build candidate artifact manifest: %w", err)
	}
	manifestDescriptor, err := s.manager.PutManifest(ctx, manifest)
	if err != nil {
		return "", "", fmt.Errorf("store candidate artifact manifest: %w", err)
	}

	var parents []artifactstore.Digest
	var previous sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT artifact_version_digest
		FROM workspace_snapshots
		WHERE workspace_id = $1 AND artifact_version_digest IS NOT NULL
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("load previous artifact version: %w", err)
	}
	if previous.Valid {
		if digest, parseErr := artifactstore.ParseDigest(previous.String); parseErr == nil {
			parents = append(parents, digest)
		}
	}

	version := artifactstore.Version{
		Schema:   artifactstore.VersionSchemaV1,
		Parents:  parents,
		Manifest: manifestDescriptor.Digest,
		Author: artifactstore.ActorIdentity{
			ID:   runID,
			Name: agentRole,
			Type: "agent_run",
		},
		Message:   "agent candidate " + runID,
		CreatedAt: time.Now().UTC(),
		Provenance: artifactstore.Provenance{
			TaskID:         taskID,
			AgentID:        runID,
			WorkspaceID:    workspaceID,
			SourceRevision: sourceRevision,
		},
	}
	versionDescriptor, err := s.manager.PutVersion(ctx, version)
	if err != nil {
		return "", "", fmt.Errorf("store candidate artifact version: %w", err)
	}
	return manifestDescriptor.Digest.String(), versionDescriptor.Digest.String(), nil
}

func (s *ArtifactSnapshotter) currentArtifactTree(ctx context.Context, workspaceID string) (map[string]artifactstore.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT logical_path, is_tombstone, artifact_json
		FROM artifacts
		WHERE workspace_id = $1 AND logical_path IS NOT NULL
		ORDER BY created_at ASC, id ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list candidate artifact history: %w", err)
	}
	defer rows.Close()

	tree := map[string]artifactstore.Artifact{}
	for rows.Next() {
		var (
			logicalPath string
			tombstone   bool
			raw         sql.NullString
		)
		if err := rows.Scan(&logicalPath, &tombstone, &raw); err != nil {
			return nil, err
		}
		if tombstone {
			delete(tree, logicalPath)
			continue
		}
		if !raw.Valid {
			continue
		}
		var artifact artifactstore.Artifact
		if err := json.Unmarshal([]byte(raw.String), &artifact); err != nil {
			return nil, fmt.Errorf("decode artifact %q: %w", logicalPath, err)
		}
		tree[logicalPath] = artifact
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tree, nil
}

func (s *ArtifactSnapshotter) loadRunSnapshot(ctx context.Context, runID string) (*models.WorkspaceSnapshot, error) {
	var (
		snap                                                  models.WorkspaceSnapshot
		gitCommit, changeID, manifest, version, description   sql.NullString
		metadata                                              sql.NullString
		agentRunID                                            sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, agent_run_id, git_commit, vcs_change_id,
		       artifact_manifest_digest, artifact_version_digest,
		       description, metadata, created_at
		FROM workspace_snapshots
		WHERE agent_run_id = $1
		LIMIT 1
	`, runID).Scan(
		&snap.ID, &snap.WorkspaceID, &agentRunID, &gitCommit, &changeID,
		&manifest, &version, &description, &metadata, &snap.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if agentRunID.Valid {
		snap.AgentRunID = stringPtr(agentRunID.String)
	}
	if gitCommit.Valid {
		snap.GitCommit = stringPtr(gitCommit.String)
	}
	if changeID.Valid {
		snap.VCSChangeID = stringPtr(changeID.String)
	}
	if manifest.Valid {
		snap.ArtifactManifestDigest = stringPtr(manifest.String)
	}
	if version.Valid {
		snap.ArtifactVersionDigest = stringPtr(version.String)
	}
	if description.Valid {
		snap.Description = stringPtr(description.String)
	}
	if metadata.Valid {
		snap.Metadata = json.RawMessage(metadata.String)
	}
	return &snap, nil
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return stringPtr(value)
}

func stringPtr(value string) *string { return &value }

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
