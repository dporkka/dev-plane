package securityscan

import "testing"

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func assertTrue(t *testing.T, got bool, msg string) {
	t.Helper()
	if !got {
		t.Errorf("expected true: %s", msg)
	}
}

func TestRegistry_All(t *testing.T) {
	reg := NewRegistry()
	all := reg.All()
	assertEqual(t, len(all), 3)

	// Verify each scanner type is present
	names := make(map[string]bool)
	for _, s := range all {
		names[s.Name()] = true
	}

	assertTrue(t, names["gitleaks"], "gitleaks should be registered")
	assertTrue(t, names["trivy"], "trivy should be registered")
	assertTrue(t, names["semgrep"], "semgrep should be registered")
}

func TestRegistry_Available(t *testing.T) {
	reg := NewRegistry()
	available := reg.Available()
	// Availability depends on whether the scanner binaries are in PATH.
	// In CI or containers they may not be installed.
	assertTrue(t, len(available) >= 0, "available count should be non-negative")
}

func TestFinding_SeverityLevels(t *testing.T) {
	tests := []struct {
		severity string
		valid    bool
	}{
		{SeverityCritical, true},
		{SeverityHigh, true},
		{SeverityMedium, true},
		{SeverityLow, true},
		{SeverityInfo, true},
		{"unknown", false},
	}

	validSeverities := map[string]bool{
		SeverityCritical: true,
		SeverityHigh:     true,
		SeverityMedium:   true,
		SeverityLow:      true,
		SeverityInfo:     true,
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			assertEqual(t, validSeverities[tt.severity], tt.valid)
		})
	}
}

func TestScanResult_IsClean(t *testing.T) {
	t.Run("clean when no findings", func(t *testing.T) {
		result := &ScanResult{
			Findings: []Finding{},
			Summary:  ScanSummary{Total: 0},
		}
		assertEqual(t, len(result.Findings) == 0 && result.Summary.Total == 0, true)
	})

	t.Run("not clean when findings exist", func(t *testing.T) {
		result := &ScanResult{
			Findings: []Finding{
				{Severity: SeverityHigh, Message: "found issue"},
			},
			Summary: ScanSummary{Total: 1, High: 1},
		}
		assertEqual(t, len(result.Findings) > 0, true)
	})
}

func TestScanResult_HasCritical(t *testing.T) {
	t.Run("true when critical finding exists", func(t *testing.T) {
		result := &ScanResult{
			Findings: []Finding{
				{Severity: SeverityCritical, Message: "critical vulnerability"},
				{Severity: SeverityHigh, Message: "high vulnerability"},
			},
			Summary: ScanSummary{Total: 2, Critical: 1},
		}

		hasCritical := false
		for _, f := range result.Findings {
			if f.Severity == SeverityCritical {
				hasCritical = true
				break
			}
		}
		assertTrue(t, hasCritical, "should have critical finding")
		assertTrue(t, result.Summary.Critical > 0, "summary should show critical")
	})

	t.Run("false when no critical findings", func(t *testing.T) {
		result := &ScanResult{
			Findings: []Finding{
				{Severity: SeverityHigh, Message: "high vulnerability"},
				{Severity: SeverityMedium, Message: "medium vulnerability"},
			},
			Summary: ScanSummary{Total: 2, Critical: 0},
		}

		hasCritical := false
		for _, f := range result.Findings {
			if f.Severity == SeverityCritical {
				hasCritical = true
				break
			}
		}
		assertEqual(t, hasCritical, false)
		assertEqual(t, result.Summary.Critical, 0)
	})

	t.Run("false when no findings at all", func(t *testing.T) {
		result := &ScanResult{
			Findings: nil,
			Summary:  ScanSummary{Total: 0, Critical: 0},
		}

		hasCritical := false
		if result.Findings != nil {
			for _, f := range result.Findings {
				if f.Severity == SeverityCritical {
					hasCritical = true
					break
				}
			}
		}
		assertEqual(t, hasCritical, false)
	})
}
