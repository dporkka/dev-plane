package securityscan

import (
	"encoding/json"
	"fmt"
	"strings"
)

func buildSummary(findings []Finding, filesScanned int) ScanSummary {
	summary := ScanSummary{
		Total:        len(findings),
		FilesScanned: filesScanned,
	}
	for _, finding := range findings {
		switch normalizeSeverity(finding.Severity) {
		case SeverityCritical:
			summary.Critical++
		case SeverityHigh:
			summary.High++
		case SeverityMedium:
			summary.Medium++
		case SeverityLow:
			summary.Low++
		default:
			summary.Info++
		}
	}
	return summary
}

func normalizeSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return SeverityCritical
	case "error", "high":
		return SeverityHigh
	case "warning", "warn", "medium", "moderate":
		return SeverityMedium
	case "low":
		return SeverityLow
	case "note", "info", "informational":
		return SeverityInfo
	default:
		return SeverityInfo
	}
}

func normalizeConfidence(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case ConfidenceHigh:
		return ConfidenceHigh
	case ConfidenceMedium:
		return ConfidenceMedium
	case ConfidenceLow:
		return ConfidenceLow
	default:
		return ConfidenceMedium
	}
}

func parseGitleaksJSON(data []byte) ([]Finding, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return []Finding{}, nil
	}

	var leaks []struct {
		Description string   `json:"Description"`
		File        string   `json:"File"`
		StartLine   int      `json:"StartLine"`
		RuleID      string   `json:"RuleID"`
		Tags        []string `json:"Tags"`
	}
	if err := json.Unmarshal(data, &leaks); err != nil {
		return nil, fmt.Errorf("parse gitleaks json: %w", err)
	}

	findings := make([]Finding, 0, len(leaks))
	for _, leak := range leaks {
		rule := leak.RuleID
		if rule == "" {
			rule = "gitleaks.secret"
		}
		message := leak.Description
		if message == "" {
			message = "secret detected"
		}
		findings = append(findings, Finding{
			Severity:   SeverityHigh,
			Rule:       rule,
			File:       leak.File,
			Line:       leak.StartLine,
			Message:    message,
			Confidence: ConfidenceHigh,
			Fix:        "Remove the secret from source control, rotate the exposed credential, and store it in the secret manager.",
		})
	}
	return findings, nil
}

func parseTrivyJSON(data []byte) ([]Finding, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return []Finding{}, nil
	}

	var report struct {
		Results []struct {
			Target          string `json:"Target"`
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Severity         string `json:"Severity"`
				Title            string `json:"Title"`
				Description      string `json:"Description"`
			} `json:"Vulnerabilities"`
			Misconfigurations []struct {
				ID            string `json:"ID"`
				Title         string `json:"Title"`
				Description   string `json:"Description"`
				Message       string `json:"Message"`
				Severity      string `json:"Severity"`
				Resolution    string `json:"Resolution"`
				CauseMetadata struct {
					StartLine int    `json:"StartLine"`
					Location  string `json:"Location"`
				} `json:"CauseMetadata"`
			} `json:"Misconfigurations"`
			Secrets []struct {
				RuleID    string `json:"RuleID"`
				Category  string `json:"Category"`
				Severity  string `json:"Severity"`
				Title     string `json:"Title"`
				StartLine int    `json:"StartLine"`
			} `json:"Secrets"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse trivy json: %w", err)
	}

	var findings []Finding
	for _, result := range report.Results {
		for _, vuln := range result.Vulnerabilities {
			message := firstNonEmpty(vuln.Title, vuln.Description, fmt.Sprintf("vulnerable package %s", vuln.PkgName))
			fix := ""
			if vuln.FixedVersion != "" {
				fix = fmt.Sprintf("Upgrade %s from %s to %s.", vuln.PkgName, vuln.InstalledVersion, vuln.FixedVersion)
			}
			findings = append(findings, Finding{
				Severity:   normalizeSeverity(vuln.Severity),
				Rule:       firstNonEmpty(vuln.VulnerabilityID, "trivy.vulnerability"),
				File:       result.Target,
				Message:    message,
				Confidence: ConfidenceHigh,
				Fix:        fix,
			})
		}
		for _, misconfig := range result.Misconfigurations {
			findings = append(findings, Finding{
				Severity:   normalizeSeverity(misconfig.Severity),
				Rule:       firstNonEmpty(misconfig.ID, "trivy.misconfiguration"),
				File:       firstNonEmpty(misconfig.CauseMetadata.Location, result.Target),
				Line:       misconfig.CauseMetadata.StartLine,
				Message:    firstNonEmpty(misconfig.Message, misconfig.Title, misconfig.Description, "misconfiguration detected"),
				Confidence: ConfidenceHigh,
				Fix:        misconfig.Resolution,
			})
		}
		for _, secret := range result.Secrets {
			findings = append(findings, Finding{
				Severity:   normalizeSeverity(firstNonEmpty(secret.Severity, SeverityHigh)),
				Rule:       firstNonEmpty(secret.RuleID, secret.Category, "trivy.secret"),
				File:       result.Target,
				Line:       secret.StartLine,
				Message:    firstNonEmpty(secret.Title, "secret detected"),
				Confidence: ConfidenceHigh,
				Fix:        "Remove the secret from source control, rotate the exposed credential, and store it in the secret manager.",
			})
		}
	}
	return findings, nil
}

func parseSemgrepJSON(data []byte) ([]Finding, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return []Finding{}, nil
	}

	var report struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
			Extra struct {
				Message  string `json:"message"`
				Severity string `json:"severity"`
				Fix      string `json:"fix"`
				Metadata struct {
					Confidence string `json:"confidence"`
				} `json:"metadata"`
			} `json:"extra"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse semgrep json: %w", err)
	}

	findings := make([]Finding, 0, len(report.Results))
	for _, result := range report.Results {
		findings = append(findings, Finding{
			Severity:   normalizeSeverity(result.Extra.Severity),
			Rule:       firstNonEmpty(result.CheckID, "semgrep.rule"),
			File:       result.Path,
			Line:       result.Start.Line,
			Message:    firstNonEmpty(result.Extra.Message, "static analysis finding"),
			Confidence: normalizeConfidence(result.Extra.Metadata.Confidence),
			Fix:        result.Extra.Fix,
		})
	}
	return findings, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
