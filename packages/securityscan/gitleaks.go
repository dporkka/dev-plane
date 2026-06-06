package securityscan

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// GitleaksScanner adapts the Gitleaks secret detection tool.
type GitleaksScanner struct {
	binary string
}

// NewGitleaksScanner creates a new Gitleaks scanner adapter.
func NewGitleaksScanner() *GitleaksScanner {
	return &GitleaksScanner{binary: "gitleaks"}
}

// Name returns the scanner name.
func (s *GitleaksScanner) Name() string {
	return "gitleaks"
}

// IsAvailable returns true if the gitleaks binary is in PATH.
func (s *GitleaksScanner) IsAvailable() bool {
	_, err := exec.LookPath(s.binary)
	return err == nil
}

// Scan runs gitleaks detect on the workspace.
func (s *GitleaksScanner) Scan(ctx context.Context, req ScanRequest) (*ScanResult, error) {
	if !s.IsAvailable() {
		return nil, fmt.Errorf("gitleaks is not installed; install from https://github.com/gitleaks/gitleaks")
	}

	start := time.Now()

	// Build command arguments
	args := []string{"detect", "--source", req.WorkspacePath, "--verbose", "--exit-code=0"}
	if len(req.Files) > 0 {
		// Gitleaks doesn't support per-file scanning directly;
		// it scans the entire source path.
		args = append(args, "--no-git")
	}

	cmd := exec.CommandContext(ctx, s.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("gitleaks scan timed out: %w", ctx.Err())
	}

	duration := time.Since(start)

	// TODO: Parse gitleaks SARIF/JSON output into structured findings.
	// For now, return the raw output as a stub.
	return &ScanResult{
		ScannerName: s.Name(),
		Findings:    nil,
		Summary: ScanSummary{
			Total:        0,
			FilesScanned: len(req.Files),
		},
		Duration:  duration,
		RawOutput: string(output),
	}, nil
}
