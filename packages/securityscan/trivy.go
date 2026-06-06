package securityscan

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// TrivyScanner adapts the Trivy vulnerability scanner.
type TrivyScanner struct {
	binary string
}

// NewTrivyScanner creates a new Trivy scanner adapter.
func NewTrivyScanner() *TrivyScanner {
	return &TrivyScanner{binary: "trivy"}
}

// Name returns the scanner name.
func (s *TrivyScanner) Name() string {
	return "trivy"
}

// IsAvailable returns true if the trivy binary is in PATH.
func (s *TrivyScanner) IsAvailable() bool {
	_, err := exec.LookPath(s.binary)
	return err == nil
}

// Scan runs trivy filesystem scan on the workspace.
func (s *TrivyScanner) Scan(ctx context.Context, req ScanRequest) (*ScanResult, error) {
	if !s.IsAvailable() {
		return nil, fmt.Errorf("trivy is not installed; install from https://aquasecurity.github.io/trivy")
	}

	start := time.Now()

	// Build command arguments
	args := []string{
		"fs",
		"--scanners", "vuln,secret,misconfig",
		"--exit-code", "0",
		"--format", "json",
		req.WorkspacePath,
	}

	cmd := exec.CommandContext(ctx, s.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("trivy scan timed out: %w", ctx.Err())
	}

	duration := time.Since(start)

	// TODO: Parse trivy JSON output into structured findings.
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
