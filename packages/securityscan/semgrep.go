package securityscan

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// SemgrepScanner adapts the Semgrep static analysis tool.
type SemgrepScanner struct {
	binary string
}

// NewSemgrepScanner creates a new Semgrep scanner adapter.
func NewSemgrepScanner() *SemgrepScanner {
	return &SemgrepScanner{binary: "semgrep"}
}

// Name returns the scanner name.
func (s *SemgrepScanner) Name() string {
	return "semgrep"
}

// IsAvailable returns true if the semgrep binary is in PATH.
func (s *SemgrepScanner) IsAvailable() bool {
	_, err := exec.LookPath(s.binary)
	return err == nil
}

// Scan runs semgrep on the workspace.
func (s *SemgrepScanner) Scan(ctx context.Context, req ScanRequest) (*ScanResult, error) {
	if !s.IsAvailable() {
		return nil, fmt.Errorf("semgrep is not installed; install from https://semgrep.dev")
	}

	start := time.Now()

	// Build command arguments
	args := []string{
		"--config", "auto",
		"--json",
		"--error",
		req.WorkspacePath,
	}

	cmd := exec.CommandContext(ctx, s.binary, args...)
	output, err := cmd.CombinedOutput()
	// Semgrep exits with code 1 when findings exist; this is expected behavior.
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("semgrep scan timed out: %w", ctx.Err())
	}
	if err != nil && cmd.ProcessState.ExitCode() != 1 {
		return nil, fmt.Errorf("semgrep scan failed: %w\noutput: %s", err, string(output))
	}

	duration := time.Since(start)

	findings, err := parseSemgrepJSON(output)
	if err != nil {
		return nil, err
	}

	return &ScanResult{
		ScannerName: s.Name(),
		Findings:    findings,
		Summary:     buildSummary(findings, len(req.Files)),
		Duration:    duration,
		RawOutput:   string(output),
	}, nil
}
