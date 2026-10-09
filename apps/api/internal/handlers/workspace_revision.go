package handlers

import (
	"context"

	"github.com/ai-dev-control-plane/runtimes"
)

var (
	errWorkspaceRevisionConflict = runtimes.ErrFileRevisionConflict
	errInvalidWorkspaceRevision  = runtimes.ErrInvalidFileRevision
)

func workspaceContentRevision(data []byte) string {
	return runtimes.FileContentRevision(data)
}

func validateWorkspaceRevision(expected *string) error {
	return runtimes.ValidateFileRevision(expected)
}

// Keep compatibility for the handler's existing test contract. The shared
// implementation is used by both human API writes and agent tools.
func writeLocalWorkspaceRevision(ctx context.Context, workspacePath, relativePath string, data []byte, expected *string) (string, error) {
	return runtimes.WriteLocalFileRevision(ctx, workspacePath, relativePath, data, expected)
}

func writeRuntimeWorkspaceRevision(ctx context.Context, workspaceID string, provider runtimes.Provider, sessionID, path string, data []byte, expected *string) (string, error) {
	_ = workspaceID
	return runtimes.WriteRuntimeFileRevision(ctx, provider, sessionID, path, data, expected)
}
