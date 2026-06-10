// Package spec generates technical specifications for tasks using deterministic
// task, repository, and project-configuration heuristics.
package spec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/models"
)

// Generator creates technical specs for tasks using AI or template-based heuristics.
type Generator struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewGenerator creates a new spec generator.
func NewGenerator(db *sql.DB, logger *slog.Logger) *Generator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Generator{
		db:     db,
		logger: logger.With("component", "spec_generator"),
	}
}

// Generate creates a technical spec for a task.
//
// Returns a deterministic heuristic spec using task, repository, and project
// configuration context.
func (g *Generator) Generate(ctx context.Context, taskID string) (*models.TaskSpec, error) {
	g.logger.Info("generating spec", "task_id", taskID)

	// Step 1: Load task from DB
	task, err := g.loadTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task: %w", err)
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Step 2: Load repo info from DB
	repo, err := g.loadRepository(ctx, task.RepositoryID)
	if err != nil {
		g.logger.Warn("failed to load repository, continuing without", "error", err)
	}

	// Step 3: Load project config (if available) for context
	config, err := g.loadProjectConfig(ctx, task.RepositoryID)
	if err != nil {
		g.logger.Debug("no project config found, continuing without", "error", err)
	}

	// Step 4: Build structured heuristic spec
	spec := g.generateMVPSpec(task, repo, config)

	// Step 5: Save spec to task_specs table
	if err := g.saveSpec(ctx, spec); err != nil {
		return nil, fmt.Errorf("save spec: %w", err)
	}

	// Step 6: Update task status to "spec_review"
	if err := g.updateTaskStatus(ctx, taskID, string(models.TaskStatusSpecReview)); err != nil {
		return nil, fmt.Errorf("update task status: %w", err)
	}

	g.logger.Info("spec generated successfully", "task_id", taskID, "spec_id", spec.ID)
	return spec, nil
}

// GetSpec retrieves a spec for a task.
func (g *Generator) GetSpec(ctx context.Context, taskID string) (*models.TaskSpec, error) {
	spec, err := g.loadSpecByTaskID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	return spec, nil
}

// ApproveSpec marks a spec as approved and updates task status.
func (g *Generator) ApproveSpec(ctx context.Context, taskID string, editedSpec *models.TaskSpec) error {
	g.logger.Info("approving spec", "task_id", taskID)

	// If user provided an edited spec, update it first
	if editedSpec != nil {
		if err := g.updateSpec(ctx, taskID, editedSpec); err != nil {
			return fmt.Errorf("update edited spec: %w", err)
		}
	}

	// Update task status to approved
	if err := g.updateTaskStatus(ctx, taskID, string(models.TaskStatusApproved)); err != nil {
		return fmt.Errorf("update task status to approved: %w", err)
	}

	g.logger.Info("spec approved", "task_id", taskID)
	return nil
}

// RejectSpec rejects a spec and returns task to backlog.
func (g *Generator) RejectSpec(ctx context.Context, taskID string, reason string) error {
	g.logger.Info("rejecting spec", "task_id", taskID, "reason", reason)

	// Update task status back to backlog
	if err := g.updateTaskStatus(ctx, taskID, string(models.TaskStatusBacklog)); err != nil {
		return fmt.Errorf("update task status to backlog: %w", err)
	}

	g.logger.Info("spec rejected, task returned to backlog", "task_id", taskID)
	return nil
}

// ---------------------------------------------------------------------------
// Heuristic Spec Generation
// ---------------------------------------------------------------------------

// generateMVPSpec creates a spec without AI using title analysis and heuristics.
func (g *Generator) generateMVPSpec(task *models.Task, repo *models.Repository, config *models.ProjectConfig) *models.TaskSpec {
	spec := &models.TaskSpec{
		ID:                 g.generateID(),
		TaskID:             task.ID,
		GeneratedAt:        time.Now().UTC(),
		EstimatedCost:      0,
		GeneratedBy:        "template-heuristic",
		ImplementationPlan: []string{},
		FilesToChange:      []string{},
		FilesToCreate:      []string{},
		AcceptanceCriteria: []string{},
		RequiredApprovals:  []string{},
	}

	// Build summary from task title
	spec.Summary = g.inferSummary(task)

	// Build problem statement from description
	spec.ProblemStatement = g.inferProblemStatement(task)

	// Infer files to change/create from title keywords
	spec.FilesToChange = g.inferFilesToChange(task)
	spec.FilesToCreate = g.inferFilesToCreate(task)

	// Build implementation plan
	spec.ImplementationPlan = g.buildImplementationPlan(task, config)

	// Build acceptance criteria
	spec.AcceptanceCriteria = g.buildAcceptanceCriteria(task)

	// Build test plan
	spec.TestPlan = g.buildTestPlan(task, config)

	// Build risk assessment
	spec.RiskAssessment = g.buildRiskAssessment(task, config)

	// Build rollback plan
	spec.RollbackPlan = g.buildRollbackPlan(task, repo)

	// Recommend agent based on task type
	spec.RecommendedAgent = g.recommendAgent(task)

	return spec
}

// inferSummary generates a spec summary from the task title.
func (g *Generator) inferSummary(task *models.Task) string {
	return fmt.Sprintf("Implement: %s", task.Title)
}

// inferProblemStatement infers the problem from task description.
func (g *Generator) inferProblemStatement(task *models.Task) string {
	if task.Description != nil && *task.Description != "" {
		return *task.Description
	}
	return fmt.Sprintf("Task requires implementing changes described in: %s", task.Title)
}

// inferFilesToChange guesses which files might need changes based on title keywords.
func (g *Generator) inferFilesToChange(task *models.Task) []string {
	var files []string
	title := strings.ToLower(task.Title)

	// Common keyword -> file pattern mappings
	patterns := []struct {
		keyword string
		file    string
	}{
		{"api", "api endpoints or handlers"},
		{"handler", "request handlers"},
		{"route", "routing configuration"},
		{"model", "data models"},
		{"database", "database schema or migrations"},
		{"db", "database layer files"},
		{"migration", "database migration files"},
		{"config", "configuration files"},
		{"test", "test files"},
		{"style", "CSS/styling files"},
		{"component", "UI components"},
		{"page", "page/route components"},
		{"hook", "custom React hooks"},
		{"util", "utility functions"},
		{"middleware", "middleware files"},
		{"auth", "authentication files"},
		{"docker", "Dockerfile or docker-compose"},
		{"ci", "CI/CD configuration"},
		{"docs", "documentation files"},
		{"readme", "README.md"},
	}

	for _, p := range patterns {
		if strings.Contains(title, p.keyword) {
			files = append(files, p.file)
		}
	}

	return files
}

// inferFilesToCreate guesses which new files might need to be created.
func (g *Generator) inferFilesToCreate(task *models.Task) []string {
	var files []string
	title := strings.ToLower(task.Title)

	// Detect "create", "add", "implement" keywords suggesting new files
	creationKeywords := []string{"create", "add", "new", "implement", "introduce"}
	suggestsCreation := false
	for _, kw := range creationKeywords {
		if strings.Contains(title, kw) {
			suggestsCreation = true
			break
		}
	}

	if suggestsCreation {
		// Guess the type of file based on title
		typePatterns := []struct {
			keyword string
			file    string
		}{
			{"component", "NewComponent file"},
			{"hook", "useHook file"},
			{"util", "utility file"},
			{"helper", "helper file"},
			{"middleware", "middleware file"},
			{"test", "test file(s)"},
			{"api", "API endpoint file"},
			{"handler", "handler file"},
			{"model", "model file"},
			{"migration", "migration file"},
			{"service", "service file"},
			{"config", "configuration file"},
			{"types", "type definitions file"},
		}

		for _, p := range typePatterns {
			if strings.Contains(title, p.keyword) {
				files = append(files, p.file)
			}
		}

		// If no specific pattern matched, add a generic implementation suggestion.
		if len(files) == 0 {
			files = append(files, "new implementation file(s)")
		}
	}

	// Always suggest test files for tasks that modify code
	if !strings.Contains(title, "test") && !strings.Contains(title, "docs") {
		files = append(files, "corresponding test file(s)")
	}

	return files
}

// buildImplementationPlan creates a step-by-step plan from task context.
func (g *Generator) buildImplementationPlan(task *models.Task, config *models.ProjectConfig) []string {
	plan := []string{
		"Analyze requirements and understand the task scope",
		"Review existing code that will be affected",
	}

	// Add framework-specific steps
	if config != nil && config.Framework != "" {
		plan = append(plan, fmt.Sprintf("Follow %s best practices and patterns", config.Framework))
	}

	plan = append(plan,
		"Implement the required changes",
		"Add or update tests as needed",
	)

	// Add lint/typecheck steps if commands are available
	if config != nil {
		if config.LintCommand != "" {
			plan = append(plan, fmt.Sprintf("Run linting: %s", config.LintCommand))
		}
		if config.TypecheckCommand != "" {
			plan = append(plan, fmt.Sprintf("Run type checking: %s", config.TypecheckCommand))
		}
		if config.TestCommand != "" {
			plan = append(plan, fmt.Sprintf("Run test suite: %s", config.TestCommand))
		}
	}

	plan = append(plan, "Verify changes work as expected")

	return plan
}

// buildAcceptanceCriteria generates acceptance criteria from task description.
func (g *Generator) buildAcceptanceCriteria(task *models.Task) []string {
	criteria := []string{
		fmt.Sprintf("The implementation satisfies: %s", task.Title),
		"All existing tests continue to pass",
		"New functionality has appropriate test coverage",
	}

	// Add criteria based on description if available
	if task.Description != nil && *task.Description != "" {
		desc := *task.Description
		// If description has bullet points, extract them
		lines := strings.Split(desc, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
				criteria = append(criteria, strings.TrimPrefix(strings.TrimPrefix(line, "-"), "*"))
			}
		}
	}

	criteria = append(criteria, "Code follows project style guidelines")

	return criteria
}

// buildTestPlan creates a test plan based on task and project config.
func (g *Generator) buildTestPlan(task *models.Task, config *models.ProjectConfig) string {
	var parts []string

	parts = append(parts, "## Test Plan")
	parts = append(parts, "")
	parts = append(parts, "### Unit Tests")
	parts = append(parts, "- Add unit tests for new/modified functions")
	parts = append(parts, "- Ensure edge cases are covered")
	parts = append(parts, "")

	// Add integration tests suggestion for API/handler tasks
	title := strings.ToLower(task.Title)
	if strings.Contains(title, "api") || strings.Contains(title, "endpoint") || strings.Contains(title, "handler") {
		parts = append(parts, "### Integration Tests")
		parts = append(parts, "- Test API endpoint with various inputs")
		parts = append(parts, "- Verify error handling and status codes")
		parts = append(parts, "")
	}

	// Add manual testing section
	parts = append(parts, "### Manual Verification")
	parts = append(parts, "- Verify the feature works in the development environment")
	parts = append(parts, "- Check that related functionality is not broken")
	parts = append(parts, "")

	// Add test command if available
	if config != nil && config.TestCommand != "" {
		parts = append(parts, fmt.Sprintf("### Test Command"))
		parts = append(parts, fmt.Sprintf("```bash\n%s\n```", config.TestCommand))
	}

	return strings.Join(parts, "\n")
}

// buildRiskAssessment evaluates risk based on task characteristics.
func (g *Generator) buildRiskAssessment(task *models.Task, config *models.ProjectConfig) string {
	var risks []string
	title := strings.ToLower(task.Title)

	// Risk factors
	if strings.Contains(title, "database") || strings.Contains(title, "migration") {
		risks = append(risks, "- Data migration risk: backup database before applying changes")
	}
	if strings.Contains(title, "auth") || strings.Contains(title, "authentication") || strings.Contains(title, "security") {
		risks = append(risks, "- Security risk: changes may affect access control; requires security review")
	}
	if strings.Contains(title, "api") && strings.Contains(title, "delete") || strings.Contains(title, "remove") {
		risks = append(risks, "- Breaking change risk: API changes may affect existing consumers")
	}
	if strings.Contains(title, "config") || strings.Contains(title, "environment") {
		risks = append(risks, "- Configuration risk: verify all environments have required config")
	}
	if strings.Contains(title, "docker") || strings.Contains(title, "deploy") {
		risks = append(risks, "- Deployment risk: test in staging before production deploy")
	}

	// Low-risk tasks
	if len(risks) == 0 {
		risks = append(risks, "- Low risk: localized changes with limited blast radius")
	}

	risks = append(risks, "- General: ensure tests pass before merging")

	return "## Risk Assessment\n\n" + strings.Join(risks, "\n")
}

// buildRollbackPlan creates a rollback strategy.
func (g *Generator) buildRollbackPlan(task *models.Task, repo *models.Repository) string {
	var parts []string

	parts = append(parts, "## Rollback Plan")
	parts = append(parts, "")

	if repo != nil {
		parts = append(parts, fmt.Sprintf("1. Revert the merge commit on branch `%s`", repo.DefaultBranch))
	} else {
		parts = append(parts, "1. Revert the merge commit on the target branch")
	}
	parts = append(parts, "2. If database migration was applied, create and run a down-migration")
	parts = append(parts, "3. Verify the application works correctly after rollback")
	parts = append(parts, "4. Communicate the rollback to the team")

	return strings.Join(parts, "\n")
}

// recommendAgent selects the appropriate agent type for a task.
func (g *Generator) recommendAgent(task *models.Task) string {
	title := strings.ToLower(task.Title)
	desc := ""
	if task.Description != nil {
		desc = strings.ToLower(*task.Description)
	}
	combined := title + " " + desc

	if strings.Contains(combined, "test") {
		return "test-engineer"
	}
	if strings.Contains(combined, "refactor") {
		return "refactorer"
	}
	if strings.Contains(combined, "bug") || strings.Contains(combined, "fix") {
		return "debugger"
	}
	if strings.Contains(combined, "docs") || strings.Contains(combined, "readme") {
		return "technical-writer"
	}
	if strings.Contains(combined, "api") || strings.Contains(combined, "endpoint") {
		return "backend-engineer"
	}
	if strings.Contains(combined, "ui") || strings.Contains(combined, "component") || strings.Contains(combined, "style") {
		return "frontend-engineer"
	}
	if strings.Contains(combined, "database") || strings.Contains(combined, "migration") || strings.Contains(combined, "schema") {
		return "database-engineer"
	}
	if strings.Contains(combined, "docker") || strings.Contains(combined, "deploy") || strings.Contains(combined, "ci") {
		return "devops-engineer"
	}
	if strings.Contains(combined, "security") || strings.Contains(combined, "auth") || strings.Contains(combined, "vulnerability") {
		return "security-engineer"
	}

	return "fullstack-engineer"
}

// ---------------------------------------------------------------------------
// Database operations (raw SQL for MVP — will use sqlc in production)
// ---------------------------------------------------------------------------

func (g *Generator) loadTask(ctx context.Context, taskID string) (*models.Task, error) {
	row := g.db.QueryRowContext(ctx, `
		SELECT id, project_id, repository_id, workspace_id, created_by,
		       source, source_id, title, description, status, priority,
		       risk_level, target_branch, spec, acceptance_criteria,
		       max_cost, max_runtime_minutes, approval_requirements, metadata,
		       started_at, completed_at, created_at, updated_at, deleted_at
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID)

	var task models.Task
	var wsID, srcID, desc, spec, ac, maxCost, apr, meta, started, completed, deleted sql.NullString

	err := row.Scan(
		&task.ID, &task.ProjectID, &task.RepositoryID, &wsID, &task.CreatedBy,
		&task.Source, &srcID, &task.Title, &desc, &task.Status, &task.Priority,
		&task.RiskLevel, &task.TargetBranch, &spec, &ac,
		&maxCost, &task.MaxRuntimeMinutes, &apr, &meta,
		&started, &completed, &task.CreatedAt, &task.UpdatedAt, &deleted,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if wsID.Valid {
		task.WorkspaceID = &wsID.String
	}
	if srcID.Valid {
		task.SourceID = &srcID.String
	}
	if desc.Valid {
		task.Description = &desc.String
	}
	if spec.Valid {
		task.Spec = []byte(spec.String)
	}
	if ac.Valid {
		task.AcceptanceCriteria = []byte(ac.String)
	}
	if maxCost.Valid {
		// Parse as float64
		var f float64
		fmt.Sscanf(maxCost.String, "%f", &f)
		task.MaxCost = &f
	}
	if apr.Valid {
		task.ApprovalRequirements = []byte(apr.String)
	}
	if meta.Valid {
		task.Metadata = []byte(meta.String)
	}

	return &task, nil
}

func (g *Generator) loadRepository(ctx context.Context, repoID string) (*models.Repository, error) {
	row := g.db.QueryRowContext(ctx, `
		SELECT id, project_id, github_id, owner, name, full_name, clone_url,
		       default_branch, private, connection_status, last_synced_at,
		       webhook_secret, settings, created_at, updated_at
		FROM repositories WHERE id = $1 AND deleted_at IS NULL
	`, repoID)

	var repo models.Repository
	var ghID sql.NullInt64
	var ws, settings sql.NullString
	var ls sql.NullTime

	err := row.Scan(
		&repo.ID, &repo.ProjectID, &ghID, &repo.Owner, &repo.Name, &repo.FullName,
		&repo.CloneURL, &repo.DefaultBranch, &repo.Private, &repo.ConnectionStatus,
		&ls, &ws, &settings, &repo.CreatedAt, &repo.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if ghID.Valid {
		repo.GitHubID = &ghID.Int64
	}
	if ls.Valid {
		t := ls.Time
		repo.LastSyncedAt = &t
	}
	if ws.Valid {
		s := ws.String
		repo.WebhookSecret = &s
	}
	if settings.Valid {
		repo.Settings = []byte(settings.String)
	}

	return &repo, nil
}

func (g *Generator) loadProjectConfig(ctx context.Context, repoID string) (*models.ProjectConfig, error) {
	row := g.db.QueryRowContext(ctx, `
		SELECT id, repository_id, package_manager, framework, test_command,
		       lint_command, typecheck_command, dev_command, build_command,
		       has_dockerfile, has_devcontainer, detected_at, updated_at
		FROM project_configs WHERE repository_id = $1
	`, repoID)

	var config models.ProjectConfig
	err := row.Scan(
		&config.ID, &config.RepositoryID, &config.PackageManager, &config.Framework,
		&config.TestCommand, &config.LintCommand, &config.TypecheckCommand,
		&config.DevCommand, &config.BuildCommand, &config.HasDockerfile,
		&config.HasDevcontainer, &config.DetectedAt, &config.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func (g *Generator) saveSpec(ctx context.Context, spec *models.TaskSpec) error {
	planJSON, _ := json.Marshal(spec.ImplementationPlan)
	changeJSON, _ := json.Marshal(spec.FilesToChange)
	createJSON, _ := json.Marshal(spec.FilesToCreate)
	criteriaJSON, _ := json.Marshal(spec.AcceptanceCriteria)
	approvalsJSON, _ := json.Marshal(spec.RequiredApprovals)

	_, err := g.db.ExecContext(ctx, `
		INSERT INTO task_specs (
			id, task_id, summary, problem_statement, implementation_plan,
			files_to_change, files_to_create, acceptance_criteria, test_plan,
			risk_assessment, rollback_plan, required_approvals, estimated_cost,
			recommended_agent, generated_by, generated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (task_id) DO UPDATE SET
			summary = EXCLUDED.summary,
			problem_statement = EXCLUDED.problem_statement,
			implementation_plan = EXCLUDED.implementation_plan,
			files_to_change = EXCLUDED.files_to_change,
			files_to_create = EXCLUDED.files_to_create,
			acceptance_criteria = EXCLUDED.acceptance_criteria,
			test_plan = EXCLUDED.test_plan,
			risk_assessment = EXCLUDED.risk_assessment,
			rollback_plan = EXCLUDED.rollback_plan,
			required_approvals = EXCLUDED.required_approvals,
			estimated_cost = EXCLUDED.estimated_cost,
			recommended_agent = EXCLUDED.recommended_agent,
			generated_by = EXCLUDED.generated_by,
			generated_at = EXCLUDED.generated_at
	`, spec.ID, spec.TaskID, spec.Summary, spec.ProblemStatement,
		planJSON, changeJSON, createJSON, criteriaJSON,
		spec.TestPlan, spec.RiskAssessment, spec.RollbackPlan,
		approvalsJSON, spec.EstimatedCost, spec.RecommendedAgent,
		spec.GeneratedBy, spec.GeneratedAt,
	)

	return err
}

func (g *Generator) loadSpecByTaskID(ctx context.Context, taskID string) (*models.TaskSpec, error) {
	row := g.db.QueryRowContext(ctx, `
		SELECT id, task_id, summary, problem_statement, implementation_plan,
		       files_to_change, files_to_create, acceptance_criteria, test_plan,
		       risk_assessment, rollback_plan, required_approvals, estimated_cost,
		       recommended_agent, generated_by, generated_at
		FROM task_specs WHERE task_id = $1
	`, taskID)

	var spec models.TaskSpec
	var planJSON, changeJSON, createJSON, criteriaJSON, approvalsJSON string

	err := row.Scan(
		&spec.ID, &spec.TaskID, &spec.Summary, &spec.ProblemStatement,
		&planJSON, &changeJSON, &createJSON, &criteriaJSON,
		&spec.TestPlan, &spec.RiskAssessment, &spec.RollbackPlan,
		&approvalsJSON, &spec.EstimatedCost, &spec.RecommendedAgent,
		&spec.GeneratedBy, &spec.GeneratedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(planJSON), &spec.ImplementationPlan)
	_ = json.Unmarshal([]byte(changeJSON), &spec.FilesToChange)
	_ = json.Unmarshal([]byte(createJSON), &spec.FilesToCreate)
	_ = json.Unmarshal([]byte(criteriaJSON), &spec.AcceptanceCriteria)
	_ = json.Unmarshal([]byte(approvalsJSON), &spec.RequiredApprovals)

	return &spec, nil
}

func (g *Generator) updateSpec(ctx context.Context, taskID string, spec *models.TaskSpec) error {
	planJSON, _ := json.Marshal(spec.ImplementationPlan)
	changeJSON, _ := json.Marshal(spec.FilesToChange)
	createJSON, _ := json.Marshal(spec.FilesToCreate)
	criteriaJSON, _ := json.Marshal(spec.AcceptanceCriteria)
	approvalsJSON, _ := json.Marshal(spec.RequiredApprovals)

	_, err := g.db.ExecContext(ctx, `
		UPDATE task_specs SET
			summary = $2,
			problem_statement = $3,
			implementation_plan = $4,
			files_to_change = $5,
			files_to_create = $6,
			acceptance_criteria = $7,
			test_plan = $8,
			risk_assessment = $9,
			rollback_plan = $10,
			required_approvals = $11
		WHERE task_id = $1
	`, taskID, spec.Summary, spec.ProblemStatement,
		planJSON, changeJSON, createJSON, criteriaJSON,
		spec.TestPlan, spec.RiskAssessment, spec.RollbackPlan,
		approvalsJSON,
	)

	return err
}

func (g *Generator) updateTaskStatus(ctx context.Context, taskID, status string) error {
	_, err := g.db.ExecContext(ctx, `
		UPDATE tasks SET status = $2, updated_at = $3 WHERE id = $1 AND deleted_at IS NULL
	`, taskID, status, time.Now().UTC())
	return err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (g *Generator) generateID() string {
	return uuid.New().String()
}
