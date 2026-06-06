// Package securityscan provides interfaces and adapters for security scanning tools.
//
// The Scanner interface abstracts multiple security scanning backends including
// Gitleaks (secret detection), Trivy (vulnerability scanning), and Semgrep
// (static analysis). Each adapter implements the Scanner interface and can be
// used interchangeably in the scanning pipeline.
package securityscan

import (
	"context"
	"fmt"
	"time"
)

// Severity levels for scan findings.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// Confidence levels for scan findings.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Scanner is the interface for security scanning tools.
type Scanner interface {
	// Name returns the scanner tool name.
	Name() string
	// Scan executes a security scan on the given target.
	Scan(ctx context.Context, req ScanRequest) (*ScanResult, error)
	// IsAvailable returns true if the scanner binary is installed and accessible.
	IsAvailable() bool
}

// ScanRequest contains the target to scan.
type ScanRequest struct {
	WorkspacePath string   // Local path to workspace/repository
	Files         []string // Specific files to scan (empty = all)
	RepoURL       string   // Repository URL for context
	Branch        string   // Branch being scanned
}

// ScanResult contains findings from a security scan.
type ScanResult struct {
	ScannerName string        `json:"scanner_name"`
	Findings    []Finding     `json:"findings"`
	Summary     ScanSummary   `json:"summary"`
	Duration    time.Duration `json:"duration_ms"`
	RawOutput   string        `json:"raw_output,omitempty"`
}

// Finding represents a single security finding from a scan.
type Finding struct {
	Severity   string `json:"severity"`   // critical, high, medium, low, info
	Rule       string `json:"rule"`       // Rule or check that triggered
	File       string `json:"file"`       // File path where finding occurred
	Line       int    `json:"line,omitempty"` // Line number (0 if unknown)
	Message    string `json:"message"`    // Human-readable description
	Confidence string `json:"confidence"` // high, medium, low
	Fix        string `json:"fix,omitempty"` // Suggested fix if available
}

// ScanSummary provides aggregated statistics for a scan.
type ScanSummary struct {
	Total      int `json:"total"`
	Critical   int `json:"critical"`
	High       int `json:"high"`
	Medium     int `json:"medium"`
	Low        int `json:"low"`
	Info       int `json:"info"`
	FilesScanned int `json:"files_scanned"`
}

// Registry holds all registered security scanners.
type Registry struct {
	scanners []Scanner
}

// NewRegistry creates a new scanner registry with all available scanners.
func NewRegistry() *Registry {
	return &Registry{
		scanners: []Scanner{
			NewGitleaksScanner(),
			NewTrivyScanner(),
			NewSemgrepScanner(),
		},
	}
}

// Available returns all scanners that are currently installed and ready.
func (r *Registry) Available() []Scanner {
	var available []Scanner
	for _, s := range r.scanners {
		if s.IsAvailable() {
			available = append(available, s)
		}
	}
	return available
}

// All returns all registered scanners regardless of availability.
func (r *Registry) All() []Scanner {
	return r.scanners
}

// ScanAll runs all available scanners and returns combined results.
// Errors from individual scanners are logged but do not stop the pipeline.
func (r *Registry) ScanAll(ctx context.Context, req ScanRequest) ([]*ScanResult, error) {
	scanners := r.Available()
	if len(scanners) == 0 {
		return nil, fmt.Errorf("no security scanners are available; install gitleaks, trivy, or semgrep")
	}

	results := make([]*ScanResult, 0, len(scanners))
	for _, s := range scanners {
		result, err := s.Scan(ctx, req)
		if err != nil {
			// Wrap the error with scanner name but continue with others
			result = &ScanResult{
				ScannerName: s.Name(),
				Findings:    nil,
				Summary:     ScanSummary{},
				Duration:    0,
				RawOutput:   fmt.Sprintf("scan error: %v", err),
			}
		}
		results = append(results, result)
	}
	return results, nil
}
