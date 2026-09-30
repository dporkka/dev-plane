package readiness

import "strings"

const AdmissionPolicyVersion = "task-readiness-v1"

type TaskAssessmentInput struct {
	HasSpec            bool     `json:"has_spec"`
	ImplementationPlan []string `json:"implementation_plan,omitempty"`
	FilesToChange      []string `json:"files_to_change,omitempty"`
	FilesToCreate      []string `json:"files_to_create,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	TestPlan           string   `json:"test_plan,omitempty"`
	RiskLevel          string   `json:"risk_level,omitempty"`
	RollbackPlan       string   `json:"rollback_plan,omitempty"`
	RequiredApprovals  []string `json:"required_approvals,omitempty"`
	TestCommand        string   `json:"test_command,omitempty"`
	LintCommand        string   `json:"lint_command,omitempty"`
	TypecheckCommand   string   `json:"typecheck_command,omitempty"`
	BuildCommand       string   `json:"build_command,omitempty"`
}

func AssessTask(input TaskAssessmentInput) Report {
	checks := []Check{
		taskSpecCheck(input),
		taskPlanCheck(input),
		taskScopeCheck(input),
		taskAcceptanceCheck(input),
		taskVerificationCheck(input),
		taskRiskCheck(input),
	}
	return summarize(checks)
}

func taskSpecCheck(input TaskAssessmentInput) Check {
	check := Check{ID: "spec", Title: "Task specification", Critical: true}
	if !input.HasSpec {
		check.Status = StatusBlocked
		check.Recommendation = "Generate and review a task specification before autonomous execution."
		return check
	}
	check.Status = StatusReady
	return check
}

func taskPlanCheck(input TaskAssessmentInput) Check {
	check := Check{ID: "plan", Title: "Implementation plan"}
	if len(nonEmpty(input.ImplementationPlan)) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Add an implementation plan so execution can be decomposed and reviewed."
		return check
	}
	check.Status = StatusReady
	check.Evidence = nonEmpty(input.ImplementationPlan)
	return check
}

func taskScopeCheck(input TaskAssessmentInput) Check {
	check := Check{ID: "scope", Title: "Bounded file scope"}
	files := append([]string{}, nonEmpty(input.FilesToChange)...)
	files = append(files, nonEmpty(input.FilesToCreate)...)
	files = uniqueSorted(files)
	if len(files) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Identify expected files or ownership boundaries before scheduling parallel autonomous work."
		return check
	}
	check.Status = StatusReady
	check.Evidence = files
	return check
}

func taskAcceptanceCheck(input TaskAssessmentInput) Check {
	check := Check{ID: "acceptance", Title: "Acceptance criteria", Critical: true}
	criteria := nonEmpty(input.AcceptanceCriteria)
	if len(criteria) == 0 {
		check.Status = StatusBlocked
		check.Recommendation = "Define observable acceptance criteria before autonomous execution."
		return check
	}
	check.Status = StatusReady
	check.Evidence = criteria
	return check
}

func taskVerificationCheck(input TaskAssessmentInput) Check {
	check := Check{ID: "verification", Title: "Task verification", Critical: true}
	if plan := strings.TrimSpace(input.TestPlan); plan != "" {
		check.Status = StatusReady
		check.Evidence = []string{"task test plan: " + plan}
		return check
	}
	if command := strings.TrimSpace(input.TestCommand); command != "" {
		check.Status = StatusReady
		check.Evidence = []string{"repository test command: " + command}
		return check
	}
	check.Status = StatusBlocked
	check.Recommendation = "Define a task test plan or configure a detected repository test command."
	return check
}

func taskRiskCheck(input TaskAssessmentInput) Check {
	check := Check{ID: "risk_controls", Title: "Risk controls", Critical: true}
	risk := strings.ToLower(strings.TrimSpace(input.RiskLevel))
	approvals := nonEmpty(input.RequiredApprovals)
	rollback := strings.TrimSpace(input.RollbackPlan)

	if risk == "critical" && len(approvals) == 0 {
		check.Status = StatusBlocked
		check.Recommendation = "Critical-risk tasks require an explicit human approval requirement."
		return check
	}
	if (risk == "high" || risk == "critical") && (len(approvals) == 0 || rollback == "") {
		check.Status = StatusAttention
		check.Recommendation = "High-risk tasks should define both approval requirements and a rollback plan."
		check.Evidence = approvals
		if rollback != "" {
			check.Evidence = append(check.Evidence, "rollback plan defined")
		}
		return check
	}
	check.Status = StatusReady
	if len(approvals) > 0 {
		check.Evidence = append(check.Evidence, approvals...)
	}
	if rollback != "" {
		check.Evidence = append(check.Evidence, "rollback plan defined")
	}
	return check
}

func summarize(checks []Check) Report {
	report := Report{Status: StatusReady, Checks: checks}
	for _, check := range checks {
		if check.Critical && check.Status == StatusBlocked {
			report.Status = StatusBlocked
			return report
		}
		if check.Status != StatusReady {
			report.Status = StatusAttention
		}
	}
	return report
}

func nonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
