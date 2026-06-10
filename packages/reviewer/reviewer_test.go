package reviewer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/securityscan"
)

func TestApplySecurityScanBlocksHighFindings(t *testing.T) {
	scanner := &fakeSecurityScanner{
		results: []*securityscan.ScanResult{
			{
				ScannerName: "gitleaks",
				Findings: []securityscan.Finding{
					{
						Severity: securityscan.SeverityHigh,
						Rule:     "aws-access-token",
						File:     ".env",
						Line:     3,
						Message:  "AWS access token",
						Fix:      "Rotate and remove the exposed credential.",
					},
				},
				Summary: securityscan.ScanSummary{Total: 1, High: 1},
			},
		},
	}
	reviewer := NewReviewer(nil, nil).WithSecurityScanner(scanner)
	report := &ReviewReport{
		RiskLevel:     "low",
		Approvable:    true,
		Findings:      []Finding{},
		SecurityNotes: "No obvious security concerns detected in changed files.",
		DiffSummary: DiffSummary{
			Files: []FileChange{{Path: ".env"}},
		},
	}

	reviewer.applySecurityScan(context.Background(), report, "/workspace")

	if report.Approvable {
		t.Fatal("expected report to be non-approvable")
	}
	if report.RiskLevel != "high" {
		t.Fatalf("risk level = %q, want high", report.RiskLevel)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(report.Findings))
	}
	finding := report.Findings[0]
	if finding.Category != "security" || finding.File != ".env" || finding.Line != 3 {
		t.Fatalf("finding = %+v", finding)
	}
	if !strings.Contains(report.SecurityNotes, "gitleaks=1") {
		t.Fatalf("security notes missing scan summary: %s", report.SecurityNotes)
	}
	if len(scanner.requests) != 1 {
		t.Fatalf("scanner requests = %d, want 1", len(scanner.requests))
	}
	if scanner.requests[0].WorkspacePath != "/workspace" {
		t.Fatalf("workspace path = %q, want /workspace", scanner.requests[0].WorkspacePath)
	}
	if len(scanner.requests[0].Files) != 1 || scanner.requests[0].Files[0] != ".env" {
		t.Fatalf("scanner files = %#v, want [.env]", scanner.requests[0].Files)
	}
}

func TestApplySecurityScanUnavailableAddsVisibleFinding(t *testing.T) {
	reviewer := NewReviewer(nil, nil).WithSecurityScanner(&fakeSecurityScanner{
		err: errors.New("no security scanners are available"),
	})
	report := &ReviewReport{
		RiskLevel:     "low",
		Approvable:    true,
		Findings:      []Finding{},
		SecurityNotes: "No obvious security concerns detected in changed files.",
		DiffSummary:   DiffSummary{Files: []FileChange{{Path: "main.go"}}},
	}

	reviewer.applySecurityScan(context.Background(), report, "/workspace")

	if !report.Approvable {
		t.Fatal("scanner unavailability should be visible but not automatically block approval")
	}
	if report.RiskLevel != "medium" {
		t.Fatalf("risk level = %q, want medium", report.RiskLevel)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(report.Findings))
	}
	if !strings.Contains(report.Findings[0].Message, "did not complete") {
		t.Fatalf("unexpected finding: %+v", report.Findings[0])
	}
	if !strings.Contains(report.SecurityNotes, "Automated security scan unavailable") {
		t.Fatalf("security notes = %s", report.SecurityNotes)
	}
}

func TestApplySecurityScanNoWorkspaceAddsVisibleFinding(t *testing.T) {
	reviewer := NewReviewer(nil, nil)
	report := &ReviewReport{
		RiskLevel:     "low",
		Approvable:    true,
		Findings:      []Finding{},
		SecurityNotes: "No obvious security concerns detected in changed files.",
	}

	reviewer.applySecurityScan(context.Background(), report, "")

	if report.RiskLevel != "medium" {
		t.Fatalf("risk level = %q, want medium", report.RiskLevel)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(report.Findings))
	}
	if !strings.Contains(report.SecurityNotes, "no local worktree path") {
		t.Fatalf("security notes = %s", report.SecurityNotes)
	}
}

type fakeSecurityScanner struct {
	results  []*securityscan.ScanResult
	err      error
	requests []securityscan.ScanRequest
}

func (s *fakeSecurityScanner) ScanAll(ctx context.Context, req securityscan.ScanRequest) ([]*securityscan.ScanResult, error) {
	s.requests = append(s.requests, req)
	return s.results, s.err
}
