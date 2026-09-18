package vcs

import (
	"context"
	"fmt"
	"time"
)

// Manager coordinates task workspaces and emits provenance only after the VCS
// operation succeeds. Recorder failures are returned to the caller: provenance
// is part of the durable task record rather than best-effort telemetry.
type Manager struct {
	backend  Backend
	recorder Recorder
	now      func() time.Time
}

func NewManager(backend Backend, recorder Recorder) (*Manager, error) {
	if backend == nil {
		return nil, fmt.Errorf("VCS backend is required")
	}
	if recorder == nil {
		recorder = NopRecorder{}
	}
	return &Manager{backend: backend, recorder: recorder, now: func() time.Time { return time.Now().UTC() }}, nil
}

type PrepareRequest struct {
	TaskID         string
	AgentID        string
	RepositoryURL  string
	RepositoryPath string
	WorkspacePath  string
	WorkspaceName  string
	Base           string
	PublishRef     string
	AuthEnv        map[string]string
}

func (m *Manager) Prepare(ctx context.Context, req PrepareRequest) (Workspace, error) {
	if req.TaskID == "" || req.AgentID == "" {
		return Workspace{}, fmt.Errorf("task ID and agent ID are required")
	}
	if req.PublishRef == "" {
		req.PublishRef = req.WorkspaceName
	}
	clone := CloneRequest{URL: req.RepositoryURL, Path: req.RepositoryPath, Env: req.AuthEnv}
	if err := m.backend.CloneOrFetch(ctx, clone); err != nil {
		return Workspace{}, fmt.Errorf("prepare repository: %w", err)
	}
	workspaceReq := WorkspaceRequest{
		RepositoryPath: req.RepositoryPath,
		WorkspacePath:  req.WorkspacePath,
		Name:           req.WorkspaceName,
		Base:           req.Base,
	}
	if err := m.backend.CreateWorkspace(ctx, workspaceReq); err != nil {
		return Workspace{}, fmt.Errorf("create task workspace: %w", err)
	}
	workspace := Workspace{
		TaskID: req.TaskID, AgentID: req.AgentID, RepositoryPath: req.RepositoryPath,
		WorkspacePath: req.WorkspacePath, Name: req.WorkspaceName, Base: req.Base, PublishRef: req.PublishRef, AuthEnv: req.AuthEnv,
	}
	if err := m.record(ctx, workspace, ProvenanceEvent{Kind: EventWorkspaceCreated}); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (m *Manager) Snapshot(ctx context.Context, workspace Workspace, message string) (Revision, error) {
	revision, err := m.backend.Snapshot(ctx, workspace.WorkspacePath, message)
	if err != nil {
		return Revision{}, err
	}
	if err := m.record(ctx, workspace, ProvenanceEvent{
		Kind: EventSnapshotCreated, CommitID: revision.CommitID, ChangeID: revision.ChangeID,
		Attributes: map[string]string{"message": message},
	}); err != nil {
		return Revision{}, err
	}
	return revision, nil
}

func (m *Manager) Publish(ctx context.Context, workspace Workspace, revision Revision) error {
	if err := m.backend.Publish(ctx, PublishRequest{WorkspacePath: workspace.WorkspacePath, Ref: workspace.PublishRef, Env: workspace.AuthEnv}); err != nil {
		return err
	}
	return m.record(ctx, workspace, ProvenanceEvent{
		Kind: EventPublished, CommitID: revision.CommitID, ChangeID: revision.ChangeID,
	})
}

func (m *Manager) Remove(ctx context.Context, workspace Workspace) error {
	req := WorkspaceRequest{
		RepositoryPath: workspace.RepositoryPath,
		WorkspacePath:  workspace.WorkspacePath,
		Name:           workspace.Name,
		Base:           workspace.Base,
	}
	if err := m.backend.RemoveWorkspace(ctx, req); err != nil {
		return err
	}
	return m.record(ctx, workspace, ProvenanceEvent{Kind: EventWorkspaceRemoved})
}

func (m *Manager) record(ctx context.Context, workspace Workspace, event ProvenanceEvent) error {
	if event.ID == "" {
		id, err := randomID()
		if err != nil {
			return fmt.Errorf("create provenance event ID: %w", err)
		}
		event.ID = id
	}
	event.Timestamp = m.now()
	event.Backend = m.backend.Name()
	event.TaskID = workspace.TaskID
	event.AgentID = workspace.AgentID
	event.Workspace = workspace.Name
	event.PublishRef = workspace.PublishRef
	if err := m.recorder.Record(ctx, event); err != nil {
		return fmt.Errorf("record provenance: %w", err)
	}
	return nil
}
