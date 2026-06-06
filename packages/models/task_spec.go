package models

import (
	"database/sql"
	"encoding/json"
	"time"
)

// TaskSpec represents a generated technical specification for a task.
// Created by the spec generator (AI or template-based for MVP).
type TaskSpec struct {
	ID                 string    `json:"id"`
	TaskID             string    `json:"task_id"`
	Summary            string    `json:"summary"`
	ProblemStatement   string    `json:"problem_statement"`
	ImplementationPlan []string  `json:"implementation_plan"`
	FilesToChange      []string  `json:"files_to_change"`
	FilesToCreate      []string  `json:"files_to_create"`
	AcceptanceCriteria []string  `json:"acceptance_criteria"`
	TestPlan           string    `json:"test_plan"`
	RiskAssessment     string    `json:"risk_assessment"`
	RollbackPlan       string    `json:"rollback_plan"`
	RequiredApprovals  []string  `json:"required_approvals"`
	EstimatedCost      float64   `json:"estimated_cost"`
	RecommendedAgent   string    `json:"recommended_agent"`
	GeneratedBy        string    `json:"generated_by"`
	GeneratedAt        time.Time `json:"generated_at"`
}

// ProjectConfig holds auto-detected project configuration from repository files.
type ProjectConfig struct {
	ID               string    `json:"id"`
	RepositoryID     string    `json:"repository_id"`
	PackageManager   string    `json:"package_manager"`
	Framework        string    `json:"framework"`
	TestCommand      string    `json:"test_command"`
	LintCommand      string    `json:"lint_command"`
	TypecheckCommand string    `json:"typecheck_command"`
	DevCommand       string    `json:"dev_command"`
	BuildCommand     string    `json:"build_command"`
	HasDockerfile    bool      `json:"has_dockerfile"`
	HasDevcontainer  bool      `json:"has_devcontainer"`
	DetectedAt       time.Time `json:"detected_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// DetectionResult stores the result of a single project detection run.
type DetectionResult struct {
	ID               string    `json:"id"`
	RepositoryID     string    `json:"repository_id"`
	WorkspaceID      *string   `json:"workspace_id,omitempty"`
	PackageManager   string    `json:"package_manager"`
	Framework        string    `json:"framework"`
	TestCommand      string    `json:"test_command"`
	LintCommand      string    `json:"lint_command"`
	TypecheckCommand string    `json:"typecheck_command"`
	DevCommand       string    `json:"dev_command"`
	BuildCommand     string    `json:"build_command"`
	HasDockerfile    bool      `json:"has_dockerfile"`
	HasDevcontainer  bool      `json:"has_devcontainer"`
	RawOutput        string    `json:"raw_output,omitempty"`
	DetectedAt       time.Time `json:"detected_at"`
}

// NullTaskSpec returns a TaskSpec from sql.Null fields.
func NullTaskSpec(
	id sql.NullString,
	taskID sql.NullString,
	summary sql.NullString,
	problemStatement sql.NullString,
	implementationPlan sql.NullString,
	filesToChange sql.NullString,
	filesToCreate sql.NullString,
	acceptanceCriteria sql.NullString,
	testPlan sql.NullString,
	riskAssessment sql.NullString,
	rollbackPlan sql.NullString,
	requiredApprovals sql.NullString,
	estimatedCost sql.NullFloat64,
	recommendedAgent sql.NullString,
	generatedBy sql.NullString,
	generatedAt sql.NullTime,
) *TaskSpec {
	ts := &TaskSpec{}
	if id.Valid {
		ts.ID = id.String
	}
	if taskID.Valid {
		ts.TaskID = taskID.String
	}
	if summary.Valid {
		ts.Summary = summary.String
	}
	if problemStatement.Valid {
		ts.ProblemStatement = problemStatement.String
	}
	if implementationPlan.Valid {
		_ = json.Unmarshal([]byte(implementationPlan.String), &ts.ImplementationPlan)
	}
	if filesToChange.Valid {
		_ = json.Unmarshal([]byte(filesToChange.String), &ts.FilesToChange)
	}
	if filesToCreate.Valid {
		_ = json.Unmarshal([]byte(filesToCreate.String), &ts.FilesToCreate)
	}
	if acceptanceCriteria.Valid {
		_ = json.Unmarshal([]byte(acceptanceCriteria.String), &ts.AcceptanceCriteria)
	}
	if testPlan.Valid {
		ts.TestPlan = testPlan.String
	}
	if riskAssessment.Valid {
		ts.RiskAssessment = riskAssessment.String
	}
	if rollbackPlan.Valid {
		ts.RollbackPlan = rollbackPlan.String
	}
	if requiredApprovals.Valid {
		_ = json.Unmarshal([]byte(requiredApprovals.String), &ts.RequiredApprovals)
	}
	if estimatedCost.Valid {
		ts.EstimatedCost = estimatedCost.Float64
	}
	if recommendedAgent.Valid {
		ts.RecommendedAgent = recommendedAgent.String
	}
	if generatedBy.Valid {
		ts.GeneratedBy = generatedBy.String
	}
	if generatedAt.Valid {
		ts.GeneratedAt = generatedAt.Time
	}
	return ts
}

// NullProjectConfig returns a ProjectConfig from sql.Null fields.
func NullProjectConfig(
	id sql.NullString,
	repositoryID sql.NullString,
	packageManager sql.NullString,
	framework sql.NullString,
	testCommand sql.NullString,
	lintCommand sql.NullString,
	typecheckCommand sql.NullString,
	devCommand sql.NullString,
	buildCommand sql.NullString,
	hasDockerfile sql.NullBool,
	hasDevcontainer sql.NullBool,
	detectedAt sql.NullTime,
	updatedAt sql.NullTime,
) *ProjectConfig {
	pc := &ProjectConfig{}
	if id.Valid {
		pc.ID = id.String
	}
	if repositoryID.Valid {
		pc.RepositoryID = repositoryID.String
	}
	if packageManager.Valid {
		pc.PackageManager = packageManager.String
	}
	if framework.Valid {
		pc.Framework = framework.String
	}
	if testCommand.Valid {
		pc.TestCommand = testCommand.String
	}
	if lintCommand.Valid {
		pc.LintCommand = lintCommand.String
	}
	if typecheckCommand.Valid {
		pc.TypecheckCommand = typecheckCommand.String
	}
	if devCommand.Valid {
		pc.DevCommand = devCommand.String
	}
	if buildCommand.Valid {
		pc.BuildCommand = buildCommand.String
	}
	if hasDockerfile.Valid {
		pc.HasDockerfile = hasDockerfile.Bool
	}
	if hasDevcontainer.Valid {
		pc.HasDevcontainer = hasDevcontainer.Bool
	}
	if detectedAt.Valid {
		pc.DetectedAt = detectedAt.Time
	}
	if updatedAt.Valid {
		pc.UpdatedAt = updatedAt.Time
	}
	return pc
}

// NullDetectionResult returns a DetectionResult from sql.Null fields.
func NullDetectionResult(
	id sql.NullString,
	repositoryID sql.NullString,
	workspaceID sql.NullString,
	packageManager sql.NullString,
	framework sql.NullString,
	testCommand sql.NullString,
	lintCommand sql.NullString,
	typecheckCommand sql.NullString,
	devCommand sql.NullString,
	buildCommand sql.NullString,
	hasDockerfile sql.NullBool,
	hasDevcontainer sql.NullBool,
	rawOutput sql.NullString,
	detectedAt sql.NullTime,
) *DetectionResult {
	dr := &DetectionResult{}
	if id.Valid {
		dr.ID = id.String
	}
	if repositoryID.Valid {
		dr.RepositoryID = repositoryID.String
	}
	if workspaceID.Valid {
		w := workspaceID.String
		dr.WorkspaceID = &w
	}
	if packageManager.Valid {
		dr.PackageManager = packageManager.String
	}
	if framework.Valid {
		dr.Framework = framework.String
	}
	if testCommand.Valid {
		dr.TestCommand = testCommand.String
	}
	if lintCommand.Valid {
		dr.LintCommand = lintCommand.String
	}
	if typecheckCommand.Valid {
		dr.TypecheckCommand = typecheckCommand.String
	}
	if devCommand.Valid {
		dr.DevCommand = devCommand.String
	}
	if buildCommand.Valid {
		dr.BuildCommand = buildCommand.String
	}
	if hasDockerfile.Valid {
		dr.HasDockerfile = hasDockerfile.Bool
	}
	if hasDevcontainer.Valid {
		dr.HasDevcontainer = hasDevcontainer.Bool
	}
	if rawOutput.Valid {
		dr.RawOutput = rawOutput.String
	}
	if detectedAt.Valid {
		dr.DetectedAt = detectedAt.Time
	}
	return dr
}
