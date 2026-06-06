package models

import "testing"

func TestWorkspaceStatus_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{WorkspaceStatusPending, "pending"},
		{WorkspaceStatusPreparing, "preparing"},
		{WorkspaceStatusReady, "ready"},
		{WorkspaceStatusRunning, "running"},
		{WorkspaceStatusStopped, "stopped"},
		{WorkspaceStatusError, "error"},
		{WorkspaceStatusDestroyed, "destroyed"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestWorkspace_Validate(t *testing.T) {
	t.Run("valid workspace", func(t *testing.T) {
		w := &Workspace{
			Name:         "test-workspace",
			Branch:       "feature/test",
			RepositoryID: "repo-1",
		}
		assertError(t, w.Validate(), false)
	})

	t.Run("missing name", func(t *testing.T) {
		w := &Workspace{
			Branch:       "feature/test",
			RepositoryID: "repo-1",
		}
		err := w.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "workspace name is required" {
			t.Errorf("got %q, want %q", err.Error(), "workspace name is required")
		}
	})

	t.Run("missing branch", func(t *testing.T) {
		w := &Workspace{
			Name:         "test-workspace",
			RepositoryID: "repo-1",
		}
		err := w.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "workspace branch is required" {
			t.Errorf("got %q, want %q", err.Error(), "workspace branch is required")
		}
	})

	t.Run("missing repository_id", func(t *testing.T) {
		w := &Workspace{
			Name:   "test-workspace",
			Branch: "feature/test",
		}
		err := w.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "workspace repository_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "workspace repository_id is required")
		}
	})
}
