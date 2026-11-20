package runtimes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Docker egress results are never written inside /workspace. Only this
// per-session, host-owned directory is mounted into the container read-only.
const (
	egressReceiptDirName  = "egress-receipts"
	egressReceiptMount    = "/run/dev-plane/egress"
	maxEgressReceiptBytes = 2 << 20
)

func safeEgressIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 80 {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func (p *DockerProvider) hostReceiptDirectory(sessionID string) (string, error) {
	if p == nil || !safeEgressIdentifier(sessionID) || p.baseDir == "" {
		return "", errors.New("invalid receipt session")
	}
	return filepath.Join(p.baseDir, sessionID, egressReceiptDirName), nil
}

// WriteEgressReceipt is the only supported privileged delivery operation.
// The sender is responsible for authenticating the run, reserving a unique
// receipt ID in durable storage, and authorizing the policy before invocation.
// The Docker runtime additionally enforces that the actual container mount
// matches this host-only directory and is read-only.
//
// Publication is create-exclusive and never exposes partial bytes: a completed,
// unreadable staging file is linked into place and then made readable.
// The sandbox cannot create, redirect, or replace a receipt there.
func (p *DockerProvider) WriteEgressReceipt(ctx context.Context, sessionID, receiptID string, data []byte) error {
	if ctx == nil || ctx.Err() != nil || !safeEgressIdentifier(receiptID) ||
		len(data) == 0 || len(data) > maxEgressReceiptBytes {
		return errors.New("invalid egress receipt request")
	}
	sess, err := p.session(sessionID)
	if err != nil {
		return err
	}
	dir, err := p.hostReceiptDirectory(sessionID)
	if err != nil {
		return err
	}
	stat, err := os.Lstat(dir)
	if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 || stat.Mode().Perm() != 0700 {
		return errors.New("untrusted or unavailable host receipt directory")
	}

	// Re-attached sessions may reference old containers that lack this mount.
	// Never silently fall back to WriteFile or an unchecked host path.
	inspectTemplate := `{{range .Mounts}}{{if eq .Destination "` + egressReceiptMount + `"}}{{.Source}}|{{.RW}}{{end}}{{end}}`
	out, err := p.runner.Run(ctx, "docker", []string{"inspect", "-f", inspectTemplate, sess.container}, commandOptions{})
	if err != nil {
		return fmt.Errorf("verify egress mount: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out.Stdout), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) != dir+"|false" {
		return errors.New("egress mount is missing, writable, or mismatched")
	}

	// A mode-0000 staging file created directly in the mounted directory
	// inherits the private SELinux container_file_t label on rootless Podman.
	// Although the agent can list it, it cannot open a mode-0000 file with
	// all capabilities dropped and a read-only bind mount. This avoids both
	// partial publication and mislabelled hardlinks from outside the mount.
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	pendingPath := filepath.Join(dir, ".pending-"+hex.EncodeToString(entropy[:]))
	stage, err := os.OpenFile(pendingPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0000)
	if err != nil {
		return fmt.Errorf("stage receipt: %w", err)
	}
	defer os.Remove(pendingPath)
	if _, err := stage.Write(data); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Link creates the final name exclusively, but it remains unreadable
	// until chmod, after all bytes have been written and synchronized.
	finalPath := filepath.Join(dir, receiptID+".json")
	if err := os.Link(pendingPath, finalPath); err != nil {
		return fmt.Errorf("publish egress receipt (already exists or unavailable): %w", err)
	}
	if err := os.Chmod(finalPath, 0600); err != nil {
		_ = os.Remove(finalPath)
		return fmt.Errorf("make completed receipt readable: %w", err)
	}
	// The image's default UID may not match the host writer's UID, especially
	// with rootful Docker and --cap-drop ALL. Do not report success until the
	// actual sandbox identity can read the completed artifact. Never cat or
	// log response bytes during this check.
	artifact := egressReceiptMount + "/" + receiptID + ".json"
	_, err = p.runner.Run(ctx, "docker", []string{
		"exec", sess.container, "sh", "-c", "test -r \"$1\"", "sh", artifact,
	}, commandOptions{})
	if err != nil {
		if removeErr := os.Remove(finalPath); removeErr != nil {
			return fmt.Errorf("container cannot read receipt and cleanup failed: %w", removeErr)
		}
		return fmt.Errorf("container cannot read host receipt: %w", err)
	}
	return nil
}
