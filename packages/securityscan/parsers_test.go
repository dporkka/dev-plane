package securityscan

import "testing"

func TestParseGitleaksJSON(t *testing.T) {
	data := []byte(`[
  {
    "Description": "AWS Access Key",
    "File": "config/.env",
    "StartLine": 4,
    "RuleID": "aws-access-token"
  }
]`)

	findings, err := parseGitleaksJSON(data)
	if err != nil {
		t.Fatalf("parse gitleaks: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	finding := findings[0]
	assertEqual(t, finding.Severity, SeverityHigh)
	assertEqual(t, finding.Rule, "aws-access-token")
	assertEqual(t, finding.File, "config/.env")
	assertEqual(t, finding.Line, 4)
	assertEqual(t, finding.Confidence, ConfidenceHigh)

	summary := buildSummary(findings, 3)
	assertEqual(t, summary.Total, 1)
	assertEqual(t, summary.High, 1)
	assertEqual(t, summary.FilesScanned, 3)
}

func TestParseTrivyJSON(t *testing.T) {
	data := []byte(`{
  "Results": [
    {
      "Target": "package-lock.json",
      "Vulnerabilities": [
        {
          "VulnerabilityID": "CVE-2026-0001",
          "PkgName": "left-pad",
          "InstalledVersion": "1.0.0",
          "FixedVersion": "1.0.1",
          "Severity": "CRITICAL",
          "Title": "Prototype pollution"
        }
      ]
    },
    {
      "Target": "Dockerfile",
      "Misconfigurations": [
        {
          "ID": "DS002",
          "Title": "Root user",
          "Severity": "HIGH",
          "Message": "Container runs as root",
          "Resolution": "Add a non-root USER",
          "CauseMetadata": {"StartLine": 7, "Location": "Dockerfile"}
        }
      ],
      "Secrets": [
        {
          "RuleID": "private-key",
          "Severity": "HIGH",
          "Title": "Private key detected",
          "StartLine": 12
        }
      ]
    }
  ]
}`)

	findings, err := parseTrivyJSON(data)
	if err != nil {
		t.Fatalf("parse trivy: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %d, want 3: %#v", len(findings), findings)
	}
	assertEqual(t, findings[0].Rule, "CVE-2026-0001")
	assertEqual(t, findings[0].Severity, SeverityCritical)
	assertEqual(t, findings[0].File, "package-lock.json")
	assertEqual(t, findings[1].Rule, "DS002")
	assertEqual(t, findings[1].Line, 7)
	assertEqual(t, findings[2].Rule, "private-key")

	summary := buildSummary(findings, 2)
	assertEqual(t, summary.Total, 3)
	assertEqual(t, summary.Critical, 1)
	assertEqual(t, summary.High, 2)
}

func TestParseSemgrepJSON(t *testing.T) {
	data := []byte(`{
  "results": [
    {
      "check_id": "go.lang.security.audit.crypto.bad",
      "path": "server.go",
      "start": {"line": 22},
      "extra": {
        "message": "weak cryptographic primitive",
        "severity": "ERROR",
        "fix": "Use a stronger primitive",
        "metadata": {"confidence": "HIGH"}
      }
    },
    {
      "check_id": "typescript.react.security",
      "path": "app.tsx",
      "start": {"line": 9},
      "extra": {
        "message": "unsafe HTML",
        "severity": "WARNING",
        "metadata": {"confidence": "LOW"}
      }
    }
  ]
}`)

	findings, err := parseSemgrepJSON(data)
	if err != nil {
		t.Fatalf("parse semgrep: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	assertEqual(t, findings[0].Severity, SeverityHigh)
	assertEqual(t, findings[0].Confidence, ConfidenceHigh)
	assertEqual(t, findings[0].Fix, "Use a stronger primitive")
	assertEqual(t, findings[1].Severity, SeverityMedium)
	assertEqual(t, findings[1].Confidence, ConfidenceLow)
}

func TestParsersRejectMalformedJSON(t *testing.T) {
	for name, parse := range map[string]func([]byte) ([]Finding, error){
		"gitleaks": parseGitleaksJSON,
		"trivy":    parseTrivyJSON,
		"semgrep":  parseSemgrepJSON,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(`{not-json`)); err == nil {
				t.Fatal("expected malformed json error")
			}
		})
	}
}

func TestParsersAcceptEmptyOutput(t *testing.T) {
	for name, parse := range map[string]func([]byte) ([]Finding, error){
		"gitleaks": parseGitleaksJSON,
		"trivy":    parseTrivyJSON,
		"semgrep":  parseSemgrepJSON,
	} {
		t.Run(name, func(t *testing.T) {
			findings, err := parse(nil)
			if err != nil {
				t.Fatalf("parse empty output: %v", err)
			}
			if len(findings) != 0 {
				t.Fatalf("findings = %d, want 0", len(findings))
			}
		})
	}
}
