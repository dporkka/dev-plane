package reviewer

import (
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/securityscan"
)

func TestParseDiffStats(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,7 @@
 package main

+import "fmt"
+
 func main() {
+	fmt.Println("hello")
 }
diff --git a/README.md b/README.md
new file mode 100644
--- /dev/null
+++ b/README.md
@@ -0,0 +1,2 @@
+# Project
+
+Welcome.
`

	summary := parseDiffStats(diff)
	if summary.FilesChanged != 2 {
		t.Errorf("files changed = %d, want 2", summary.FilesChanged)
	}
	if len(summary.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(summary.Files))
	}

	mainFile := summary.Files[0]
	if mainFile.Path != "main.go" {
		t.Errorf("first file path = %q, want main.go", mainFile.Path)
	}
	if mainFile.Status != "modified" {
		t.Errorf("first file status = %q, want modified", mainFile.Status)
	}

	readmeFile := summary.Files[1]
	if readmeFile.Path != "README.md" {
		t.Errorf("second file path = %q, want README.md", readmeFile.Path)
	}
	if readmeFile.Status != "added" {
		t.Errorf("second file status = %q, want added", readmeFile.Status)
	}
}

func TestParseDiffStats_DeletedFile(t *testing.T) {
	diff := `diff --git a/old.go b/old.go
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-line1
-line2
-line3
`

	summary := parseDiffStats(diff)
	if len(summary.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(summary.Files))
	}
	if summary.Files[0].Status != "deleted" {
		t.Errorf("status = %q, want deleted", summary.Files[0].Status)
	}
}

func TestParseDiffStats_Empty(t *testing.T) {
	summary := parseDiffStats("")
	if summary.FilesChanged != 0 {
		t.Errorf("files changed = %d, want 0", summary.FilesChanged)
	}
	if len(summary.Files) != 0 {
		t.Errorf("files = %d, want 0", len(summary.Files))
	}
}

func TestClassifyFile(t *testing.T) {
	tests := []struct {
		path        string
		isTest      bool
		isConfig    bool
		isMigration bool
	}{
		{"main.go", false, false, false},
		{"main_test.go", true, false, false},
		{"package.json", false, true, false},
		{"Dockerfile", false, true, false},
		{"migrations/001_create_users.sql", false, false, true},
		{"V1__init.sql", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			isTest, isConfig, isMigration := classifyFile(tt.path)
			if isTest != tt.isTest {
				t.Errorf("isTest = %v, want %v", isTest, tt.isTest)
			}
			if isConfig != tt.isConfig {
				t.Errorf("isConfig = %v, want %v", isConfig, tt.isConfig)
			}
			if isMigration != tt.isMigration {
				t.Errorf("isMigration = %v, want %v", isMigration, tt.isMigration)
			}
		})
	}
}

func TestExtractLineNumber(t *testing.T) {
	tests := []struct {
		hunk string
		want int
	}{
		{"@@ -10,5 +20,7 @@ func main() {", 20},
		{"@@ -1 +1 @@", 1},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.hunk, func(t *testing.T) {
			if got := extractLineNumber(tt.hunk); got != tt.want {
				t.Errorf("line number = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNormalizeReviewSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"critical", "critical"},
		{"HIGH", "high"},
		{"Medium", "medium"},
		{"low", "low"},
		{"unknown", "info"},
		{"", "info"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeReviewSeverity(tt.input); got != tt.want {
				t.Errorf("severity = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChangedFilePaths(t *testing.T) {
	summary := DiffSummary{
		Files: []FileChange{
			{Path: "a.go"},
			{Path: ""},
			{Path: "b.go"},
		},
	}
	paths := changedFilePaths(summary)
	if len(paths) != 2 {
		t.Errorf("paths = %v, want 2 entries", paths)
	}
}

func TestAppendSecurityNote(t *testing.T) {
	if got := appendSecurityNote("", "note one"); got != "note one" {
		t.Errorf("got %q, want note one", got)
	}
	if got := appendSecurityNote("existing.", "note two"); got != "existing. note two" {
		t.Errorf("got %q, want existing. note two", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "b", "c"); got != "b" {
		t.Errorf("got %q, want b", got)
	}
	if got := firstNonEmpty("", "  ", "c"); got != "c" {
		t.Errorf("got %q, want c", got)
	}
	if got := firstNonEmpty("", "  "); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestCheckSecurity(t *testing.T) {
	t.Run("sensitive files", func(t *testing.T) {
		summary := DiffSummary{
			Files: []FileChange{
				{Path: "config/secrets.yaml"},
				{Path: ".env.local"},
			},
		}
		notes := (&Reviewer{}).checkSecurity(summary)
		if !strings.Contains(notes, "secrets.yaml") {
			t.Errorf("notes missing secrets.yaml: %q", notes)
		}
		if !strings.Contains(notes, ".env.local") {
			t.Errorf("notes missing .env.local: %q", notes)
		}
	})

	t.Run("no concerns", func(t *testing.T) {
		summary := DiffSummary{Files: []FileChange{{Path: "main.go"}}}
		notes := (&Reviewer{}).checkSecurity(summary)
		if notes != "No obvious security concerns detected in changed files." {
			t.Errorf("notes = %q", notes)
		}
	})
}

func TestGenerateReview(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,4 @@
 package main

+func newFunc() {}
+
 func main() {}
`

	r := NewReviewer(nil, nil)
	report := r.generateReview(diff, nil, nil)

	if report.RunID == "" {
		t.Error("expected run id to be generated")
	}
	if report.Summary == "" {
		t.Error("expected summary")
	}
	if report.RiskLevel != "medium" {
		t.Errorf("risk level = %q, want medium", report.RiskLevel)
	}
	if !report.Approvable {
		t.Error("expected approvable")
	}
	if report.TestCoverage != "No tests detected" {
		t.Errorf("test coverage = %q, want No tests detected", report.TestCoverage)
	}
}

func TestGenerateReview_LargeChange(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n"
	for i := 0; i < 30; i++ {
		diff += "+line\n"
	}

	r := NewReviewer(nil, nil)
	report := r.generateReview(diff, nil, nil)

	if report.RiskLevel != "medium" {
		t.Errorf("risk level = %q, want medium", report.RiskLevel)
	}
}

func TestGenerateReview_WithFailingTests(t *testing.T) {
	results := &TestResults{
		TestsPassed:     false,
		TestsTotal:      10,
		TestsFailed:     3,
		LintPassed:      false,
		TypecheckPassed: false,
		CoveragePercent: 45.5,
	}

	r := NewReviewer(nil, nil)
	report := r.generateReview("", results, nil)

	if report.Approvable {
		t.Error("expected not approvable when tests fail")
	}
	if report.RiskLevel != "critical" {
		t.Errorf("risk level = %q, want critical", report.RiskLevel)
	}
	if report.TestCoverage != "45.5% coverage" {
		t.Errorf("test coverage = %q, want 45.5%% coverage", report.TestCoverage)
	}
}

func TestGenerateReview_WithMigration(t *testing.T) {
	diff := `diff --git a/migrations/001_add_users.sql b/migrations/001_add_users.sql
--- /dev/null
+++ b/migrations/001_add_users.sql
@@ -0,0 +1,3 @@
+CREATE TABLE users ();
+ALTER TABLE users;
+DROP TABLE users;
`

	r := NewReviewer(nil, nil)
	report := r.generateReview(diff, nil, nil)

	if report.RiskLevel != "high" {
		t.Errorf("risk level = %q, want high", report.RiskLevel)
	}
	if report.Approvable {
		t.Error("expected not approvable with high risk migration")
	}
}

func TestReviewerFindingFromScan(t *testing.T) {
	finding := reviewerFindingFromScan("gitleaks", securityscan.Finding{
		Severity: "high",
		Rule:     "aws-access-key",
		File:     "config.env",
		Line:     5,
		Message:  "AWS key found",
		Fix:      "rotate key",
	})

	if finding.Severity != "high" {
		t.Errorf("severity = %q, want high", finding.Severity)
	}
	if finding.Category != "security" {
		t.Errorf("category = %q, want security", finding.Category)
	}
	if finding.File != "config.env" {
		t.Errorf("file = %q, want config.env", finding.File)
	}
	if !strings.Contains(finding.Suggestion, "rotate key") {
		t.Errorf("suggestion = %q", finding.Suggestion)
	}
}

func TestAddSecurityFinding(t *testing.T) {
	r := NewReviewer(nil, nil)

	t.Run("critical", func(t *testing.T) {
		report := &ReviewReport{RiskLevel: "low", Approvable: true}
		r.addSecurityFinding(report, Finding{Severity: "critical"})
		if report.RiskLevel != "critical" || report.Approvable {
			t.Errorf("report = %+v", report)
		}
	})

	t.Run("high", func(t *testing.T) {
		report := &ReviewReport{RiskLevel: "low", Approvable: true}
		r.addSecurityFinding(report, Finding{Severity: "high"})
		if report.RiskLevel != "high" || report.Approvable {
			t.Errorf("report = %+v", report)
		}
	})

	t.Run("medium", func(t *testing.T) {
		report := &ReviewReport{RiskLevel: "low", Approvable: true}
		r.addSecurityFinding(report, Finding{Severity: "medium"})
		if report.RiskLevel != "medium" || !report.Approvable {
			t.Errorf("report = %+v", report)
		}
	})
}

func TestGenerateReview_IncludesSteps(t *testing.T) {
	steps := []models.AgentStep{
		{StepType: models.AgentStepTypeToolCall, Status: models.AgentStepStatusCompleted},
		{StepType: models.AgentStepTypeCommandRun, Status: models.AgentStepStatusFailed},
	}

	r := NewReviewer(nil, nil)
	report := r.generateReview("", nil, steps)

	if report.RiskLevel != "medium" {
		t.Errorf("risk level = %q, want medium", report.RiskLevel)
	}
}
