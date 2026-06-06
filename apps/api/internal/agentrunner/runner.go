// Package agentrunner executes agent runs with tool-calling loops, budget
// tracking, step persistence, and event streaming.
package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/budget"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

// Runner executes agent runs with tool calling, budget checks, and event streaming.
type Runner struct {
	db       *sql.DB
	tools    *tools.WorkspaceTools
	router   *modelrouter.Router
	policies *policies.Engine
	budget   *budget.Engine
	kernel   *capability.Kernel
	eventBus *events.Bus
	logger   *slog.Logger
	runtimes map[string]runtimes.Provider
}

// NewRunner creates an agent runner with all required dependencies.
func NewRunner(db *sql.DB, workspaceTools *tools.WorkspaceTools, polEng *policies.Engine, budgetEng *budget.Engine, eventBus *events.Bus, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	kernel := capability.NewKernel(polEng, budgetEng, nil, logger)
	return &Runner{
		db:       db,
		tools:    workspaceTools,
		router:   modelrouter.NewRouter(nil, modelrouter.AllProviders()...),
		policies: polEng,
		budget:   budgetEng,
		kernel:   kernel,
		eventBus: eventBus,
		logger:   logger,
		runtimes: map[string]runtimes.Provider{},
	}
}

// WithModelRouter overrides the model router. Tests use this to provide
// deterministic fake model responses without external provider calls.
func (r *Runner) WithModelRouter(router *modelrouter.Router) *Runner {
	r.router = router
	return r
}

// WithCapabilityKernel overrides the default kernel. Tests and composed services
// can use this to provide an audit-backed kernel.
func (r *Runner) WithCapabilityKernel(kernel *capability.Kernel) *Runner {
	r.kernel = kernel
	return r
}

// WithRuntimeProvider registers a runtime provider for non-local workspace
// sessions.
func (r *Runner) WithRuntimeProvider(name string, provider runtimes.Provider) *Runner {
	if r.runtimes == nil {
		r.runtimes = map[string]runtimes.Provider{}
	}
	r.runtimes[name] = provider
	return r
}

// Run executes an agent run from start to finish.
//
// Steps:
//  1. Load AgentRun from DB, set status to "running"
//  2. Load task, workspace from DB
//  3. Get workspace path
//  4. Build system prompt from agent role
//  5. Enter agent loop (up to max steps)
//  6. Run lint/typecheck/tests via test runner
//  7. Record final results, cost, status
//  8. Publish run.completed event
func (r *Runner) Run(ctx context.Context, runID string) error {
	r.logger.Info("starting agent run", "run_id", runID)

	// 1. Load AgentRun
	run, err := r.loadAgentRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("load agent run %s: %w", runID, err)
	}

	// 2. Load task and workspace
	task, err := r.loadTask(ctx, run.TaskID)
	if err != nil {
		return r.failRun(ctx, runID, fmt.Sprintf("load task: %v", err))
	}

	workspace, err := r.loadWorkspace(ctx, run.WorkspaceID)
	if err != nil {
		return r.failRun(ctx, runID, fmt.Sprintf("load workspace: %v", err))
	}

	// 3. Determine workspace execution target
	workspacePath := r.getWorkspacePath(workspace)
	runtimeProvider, _, runtimeErr := r.runtimeProviderForWorkspace(ctx, workspace)
	if runtimeErr != nil {
		return r.failRun(ctx, runID, fmt.Sprintf("workspace runtime not accessible: %v", runtimeErr))
	}
	if runtimeProvider == nil {
		if workspacePath == "" {
			return r.failRun(ctx, runID, "workspace path is empty")
		}
		if _, err := os.Stat(workspacePath); err != nil {
			return r.failRun(ctx, runID, fmt.Sprintf("workspace path not accessible: %v", err))
		}
	}

	// 4. Mark run as running
	if err := r.updateRunStatus(ctx, runID, models.AgentRunStatusRunning, nil); err != nil {
		return fmt.Errorf("set run status to running: %w", err)
	}

	// Publish run.started event
	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.started", runID), map[string]any{
		"run_id":     runID,
		"task_id":    run.TaskID,
		"agent_role": run.AgentRole,
		"status":     "running",
		"timestamp":  time.Now().UTC(),
	})
	_ = r.publishEvent(ctx, events.StreamAgents, events.AgentRunStarted, map[string]any{
		"run_id":     runID,
		"task_id":    run.TaskID,
		"agent_role": run.AgentRole,
		"status":     models.AgentRunStatusRunning,
		"timestamp":  time.Now().UTC(),
	})

	// Build system prompt
	systemPrompt := BuildSystemPrompt(run.AgentRole, task, workspace)
	r.logger.Debug("system prompt built", "run_id", runID, "prompt_len", len(systemPrompt))

	// 5. Agent loop
	maxSteps := 50
	history := r.loadRunHistory(ctx, run.ID)
	state := seedRunState(run, history)
	startTime := time.Now()
	mailboxMessages := r.loadMailboxMessages(ctx, task.ID, run.AgentRole, 20)

	nextStepNum := nextStepNumber(history)
	maxStepNum := nextStepNum + maxSteps - 1
	for stepNum := nextStepNum; stepNum <= maxStepNum; stepNum++ {
		// Check for context cancellation
		if ctx.Err() != nil {
			return r.failRun(ctx, runID, fmt.Sprintf("context cancelled: %v", ctx.Err()))
		}

		action, modelResult, err := r.nextModelAction(ctx, run, task, systemPrompt, mailboxMessages, history)
		if err != nil {
			return r.failRun(ctx, runID, fmt.Sprintf("model action: %v", err))
		}
		state.ModelCalls++
		if modelResult != nil {
			state.CostSoFar += modelResult.Cost
			run.PromptTokens += modelResult.PromptTokens
			run.CompletionTokens += modelResult.CompletionTokens
			run.TotalCost += modelResult.Cost
			if modelResult.Model != "" {
				run.Model = &modelResult.Model
			}
			if modelResult.Provider != "" {
				run.Provider = &modelResult.Provider
			}
			if err := r.recordModelUsage(ctx, run, task, modelResult); err != nil {
				r.logger.Warn("failed to record model usage", "run_id", runID, "error", err)
			}
		}

		budgetResult, err := r.checkBudget(ctx, run.TaskID, state)
		if err != nil {
			r.logger.Error("budget check failed", "error", err)
		}
		if budgetResult != nil && !budgetResult.Allowed {
			return r.failRun(ctx, runID, fmt.Sprintf("budget exceeded: %s", budgetResult.Reason))
		}

		switch action.Action {
		case "tool_call":
			toolCall, err := action.toolCall()
			if err != nil {
				return r.failRun(ctx, runID, err.Error())
			}
			step, err := r.runToolStep(ctx, stepNum, run, task, workspace, workspacePath, toolCall)
			if err != nil {
				var decisionErr *capabilityDecisionError
				if errors.As(err, &decisionErr) && decisionErr.requiresApproval() {
					if approvalErr := r.requestCapabilityApproval(ctx, run, task, decisionErr); approvalErr != nil {
						return r.failRun(ctx, runID, fmt.Sprintf("create approval request: %v", approvalErr))
					}
					return r.pauseRun(ctx, runID, decisionErr.Error())
				}
				return r.failRun(ctx, runID, err.Error())
			}
			history = append(history, *step)

			// Update and check budget
			state.ToolCalls++
			if toolCall.Name == "run_command" || toolCall.Name == "run_tests" {
				state.ShellCommands++
			}
			if toolCall.Name == "write_file" || toolCall.Name == "apply_patch" {
				state.FilesChanged++
			}
			state.DurationMinutes = int(time.Since(startTime).Minutes())

			budgetResult, err := r.checkBudget(ctx, run.TaskID, state)
			if err != nil {
				r.logger.Error("budget check failed", "error", err)
			}
			if budgetResult != nil && !budgetResult.Allowed {
				return r.failRun(ctx, runID, fmt.Sprintf("budget exceeded: %s", budgetResult.Reason))
			}

		case "final_response":
			content := strings.TrimSpace(action.Content)
			if content == "" {
				content = "Agent reported completion."
			}
			step := &models.AgentStep{
				ID:         uuid.New().String(),
				AgentRunID: runID,
				StepNumber: stepNum,
				StepType:   models.AgentStepTypeMessage,
				Status:     models.AgentStepStatusCompleted,
				Content:    &content,
			}
			if err := r.persistStep(ctx, step); err != nil {
				r.logger.Error("failed to persist final response step", "error", err, "step", stepNum)
			}
			_ = r.streamStep(ctx, step, "completed")
			history = append(history, *step)
			r.logger.Info("agent reported completion", "run_id", runID, "steps", stepNum)
			stepNum = maxStepNum

		case "handoff":
			content := strings.TrimSpace(action.Content)
			if content == "" {
				return r.failRun(ctx, runID, "handoff action missing content")
			}
			toAgent := strings.TrimSpace(action.ToAgent)
			if toAgent == "" {
				return r.failRun(ctx, runID, "handoff action missing to_agent")
			}
			messageType := strings.TrimSpace(action.MessageType)
			if messageType == "" {
				messageType = models.MessageTypeHandoff
			}
			if err := r.persistAgentMessage(ctx, task.ID, run.ID, run.AgentRole, toAgent, messageType, content, action.Metadata); err != nil {
				return r.failRun(ctx, runID, fmt.Sprintf("persist handoff: %v", err))
			}
			stepContent := fmt.Sprintf("Handoff to %s: %s", toAgent, content)
			step := &models.AgentStep{
				ID:         uuid.New().String(),
				AgentRunID: runID,
				StepNumber: stepNum,
				StepType:   models.AgentStepTypeMessage,
				Status:     models.AgentStepStatusCompleted,
				Content:    &stepContent,
			}
			if err := r.persistStep(ctx, step); err != nil {
				r.logger.Error("failed to persist handoff step", "error", err, "step", stepNum)
			}
			_ = r.streamStep(ctx, step, "completed")
			history = append(history, *step)
			r.logger.Info("agent handoff persisted", "run_id", runID, "from_agent", run.AgentRole, "to_agent", toAgent)
			stepNum = maxStepNum

		case "request_approval":
			reason := strings.TrimSpace(action.Content)
			if reason == "" {
				reason = "Agent requested human approval."
			}
			step := &models.AgentStep{
				ID:         uuid.New().String(),
				AgentRunID: runID,
				StepNumber: stepNum,
				StepType:   models.AgentStepTypeApprovalRequest,
				Status:     models.AgentStepStatusCompleted,
				Content:    &reason,
			}
			if err := r.persistStep(ctx, step); err != nil {
				r.logger.Error("failed to persist approval request step", "error", err, "step", stepNum)
			}
			_ = r.streamStep(ctx, step, "completed")
			if err := r.requestModelApproval(ctx, run, task, reason); err != nil {
				return r.failRun(ctx, runID, fmt.Sprintf("create approval request: %v", err))
			}
			return r.pauseRun(ctx, runID, reason)

		default:
			return r.failRun(ctx, runID, fmt.Sprintf("unknown model action: %s", action.Action))
		}
	}

	// 6. Run lint/typecheck/tests via test runner
	r.logger.Info("running final checks", "run_id", runID)
	testResults := r.runFinalChecks(ctx, run, task, workspace, workspacePath)

	// 7. Get git diff for summary
	diffOutput, _ := r.executeTool(ctx, run, task, workspace, workspacePath, "get_git_diff", json.RawMessage(`{}`))

	// Build summary
	summary := r.buildSummary(state, testResults, diffOutput)

	// 8. Mark run as completed
	now := time.Now().UTC()
	r.updateRunCompletion(ctx, runID, models.AgentRunStatusCompleted, summary, state)

	// Publish run.completed event
	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.completed", runID), map[string]any{
		"run_id":     runID,
		"task_id":    run.TaskID,
		"agent_role": run.AgentRole,
		"status":     models.AgentRunStatusCompleted,
		"steps":      state.ToolCalls,
		"summary":    summary,
		"timestamp":  now,
	})
	_ = r.publishEvent(ctx, events.StreamAgents, events.AgentRunCompleted, map[string]any{
		"run_id":     runID,
		"task_id":    run.TaskID,
		"agent_role": run.AgentRole,
		"status":     models.AgentRunStatusCompleted,
		"steps":      state.ToolCalls,
		"summary":    summary,
		"timestamp":  now,
	})

	r.logger.Info("agent run completed", "run_id", runID, "steps", state.ToolCalls, "duration_ms", time.Since(startTime).Milliseconds())

	return nil
}

// ToolCall represents a single executable tool request selected by the model.
type ToolCall struct {
	Name  string
	Input json.RawMessage
}

type modelAction struct {
	Action      string          `json:"action"`
	ToolName    string          `json:"tool_name,omitempty"`
	ToolInput   json.RawMessage `json:"tool_input,omitempty"`
	Content     string          `json:"content,omitempty"`
	ToAgent     string          `json:"to_agent,omitempty"`
	MessageType string          `json:"message_type,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

func (a modelAction) toolCall() (ToolCall, error) {
	name := strings.TrimSpace(a.ToolName)
	if name == "" {
		return ToolCall{}, fmt.Errorf("tool_call action missing tool_name")
	}
	input := a.ToolInput
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage(`{}`)
	}
	if !json.Valid(input) {
		return ToolCall{}, fmt.Errorf("tool_call action has invalid tool_input JSON")
	}
	return ToolCall{Name: name, Input: input}, nil
}

func (r *Runner) nextModelAction(ctx context.Context, run *models.AgentRun, task *models.Task, systemPrompt string, mailbox []models.AgentMessage, history []models.AgentStep) (*modelAction, *modelrouter.CallResult, error) {
	if r.router == nil {
		return nil, nil, fmt.Errorf("model router is not configured")
	}
	prompt := BuildToolCallPrompt(ctx, history, mailbox, task)
	result, err := r.router.RouteCall(ctx, modelrouter.CallRequest{
		TaskType:      taskTypeForRole(run.AgentRole),
		Difficulty:    difficultyForRisk(string(task.RiskLevel)),
		LatencyReq:    modelrouter.LatencyNormal,
		ContextSize:   len(systemPrompt) + len(prompt),
		StructuredReq: true,
		Messages: []modelrouter.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, result, err
	}
	action, err := parseModelAction(result.Content)
	if err != nil {
		return nil, result, err
	}
	return action, result, nil
}

func parseModelAction(content string) (*modelAction, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	if trimmed == "" {
		return nil, fmt.Errorf("empty model action")
	}
	var action modelAction
	if err := json.Unmarshal([]byte(trimmed), &action); err != nil {
		return nil, fmt.Errorf("decode model action JSON: %w", err)
	}
	action.Action = strings.TrimSpace(action.Action)
	if action.Action == "" {
		return nil, fmt.Errorf("model action missing action")
	}
	return &action, nil
}

func taskTypeForRole(role string) string {
	switch role {
	case models.AgentRolePlanner:
		return modelrouter.TaskTypeArchitecture
	case models.AgentRoleReviewer, models.AgentRoleSecurity:
		return modelrouter.TaskTypeReview
	case models.AgentRoleTestRunner:
		return modelrouter.TaskTypeTest
	case models.AgentRoleDocs, models.AgentRoleReleaseManager:
		return modelrouter.TaskTypeDocs
	default:
		return modelrouter.TaskTypeCode
	}
}

func difficultyForRisk(risk string) string {
	switch risk {
	case string(models.RiskLevelHigh):
		return modelrouter.DifficultyHard
	case string(models.RiskLevelCritical):
		return modelrouter.DifficultyExpert
	case string(models.RiskLevelMedium):
		return modelrouter.DifficultyMedium
	default:
		return modelrouter.DifficultyEasy
	}
}

func (r *Runner) runToolStep(ctx context.Context, stepNum int, run *models.AgentRun, task *models.Task, workspace *models.Workspace, workspacePath string, toolCall ToolCall) (*models.AgentStep, error) {
	step := &models.AgentStep{
		ID:         uuid.New().String(),
		AgentRunID: run.ID,
		StepNumber: stepNum,
		StepType:   models.AgentStepTypeToolCall,
		Status:     models.AgentStepStatusRunning,
		ToolName:   &toolCall.Name,
		ToolInput:  toolCall.Input,
	}
	if err := r.persistStep(ctx, step); err != nil {
		r.logger.Error("failed to persist step", "error", err, "step", stepNum)
	}

	_ = r.streamStep(ctx, step, "started")

	stepStart := time.Now()
	output, toolErr := r.executeTool(ctx, run, task, workspace, workspacePath, toolCall.Name, toolCall.Input)
	step.LatencyMs = int(time.Since(stepStart).Milliseconds())

	step.Status = models.AgentStepStatusCompleted
	step.ToolOutput = output
	if toolErr != nil {
		step.Status = models.AgentStepStatusFailed
		errStr := toolErr.Error()
		step.Content = &errStr
		r.logger.Warn("tool execution failed", "tool", toolCall.Name, "error", toolErr)
	}

	if err := r.updateStepStatus(ctx, step); err != nil {
		r.logger.Error("failed to update step status", "error", err)
	}

	_ = r.streamStep(ctx, step, "completed")
	return step, toolErr
}

// executeTool authorizes and dispatches a tool call to WorkspaceTools.
func (r *Runner) executeTool(ctx context.Context, run *models.AgentRun, task *models.Task, workspace *models.Workspace, workspacePath, toolName string, input json.RawMessage) (json.RawMessage, error) {
	if err := r.authorizeTool(ctx, run, task, workspace, toolName, input); err != nil {
		return nil, err
	}
	if provider, sessionID, err := r.runtimeProviderForWorkspace(ctx, workspace); err != nil {
		return nil, err
	} else if provider != nil {
		return r.executeRuntimeTool(ctx, provider, sessionID, toolName, input)
	}
	return r.executeToolUnchecked(ctx, toolName, workspacePath, input)
}

// executeToolUnchecked dispatches a tool call after capability authorization.
func (r *Runner) executeToolUnchecked(ctx context.Context, toolName, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	switch toolName {
	case "read_file":
		return r.tools.ReadFile(ctx, workspacePath, input)
	case "write_file":
		return r.tools.WriteFile(ctx, workspacePath, input)
	case "search_files":
		return r.tools.SearchFiles(ctx, workspacePath, input)
	case "list_directory":
		return r.tools.ListDirectory(ctx, workspacePath, input)
	case "apply_patch":
		return r.tools.ApplyPatch(ctx, workspacePath, input)
	case "run_command":
		return r.tools.RunCommand(ctx, workspacePath, input)
	case "inspect_repo":
		return r.tools.InspectRepo(ctx, workspacePath, input)
	case "get_git_diff":
		return r.tools.GetGitDiff(ctx, workspacePath, input)
	case "create_commit":
		return r.tools.CreateCommit(ctx, workspacePath, input)
	case "run_tests":
		return r.tools.RunTests(ctx, workspacePath, input)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (r *Runner) authorizeTool(ctx context.Context, run *models.AgentRun, task *models.Task, workspace *models.Workspace, toolName string, input json.RawMessage) error {
	if r.kernel == nil {
		return fmt.Errorf("capability kernel is not configured")
	}

	operation, ok := toolOperation(toolName)
	if !ok {
		return fmt.Errorf("unknown tool: %s", toolName)
	}

	resource := toolResource(toolName, input)
	organization := r.loadTaskOrganization(ctx, task)
	req := capability.Request{
		ActorType:    "agent",
		AgentRole:    run.AgentRole,
		Organization: organization,
		Workspace:    workspace,
		Task:         task,
		AgentRun:     run,
		Operation:    operation,
		Resource:     resource,
		SandboxState: sandboxState(workspace),
		Details: map[string]any{
			"tool_name": toolName,
			"input":     string(input),
		},
	}

	result, err := r.kernel.Evaluate(ctx, req)
	if err != nil {
		return fmt.Errorf("capability evaluation failed for %s: %w", toolName, err)
	}
	if result.Effect == policies.EffectDeny {
		return &capabilityDecisionError{toolName: toolName, operation: operation, resource: resource, result: result}
	}
	if result.RequiredApproval {
		return &capabilityDecisionError{toolName: toolName, operation: operation, resource: resource, result: result}
	}
	return nil
}

func (r *Runner) loadTaskOrganization(ctx context.Context, task *models.Task) *models.Organization {
	if r.db == nil || task == nil || task.ProjectID == "" {
		return nil
	}
	var orgID string
	err := r.db.QueryRowContext(ctx, `SELECT organization_id FROM projects WHERE id = $1`, task.ProjectID).Scan(&orgID)
	if err != nil || orgID == "" {
		if err != nil {
			r.logger.Warn("failed to load task organization for capability audit", "task_id", task.ID, "project_id", task.ProjectID, "error", err)
		}
		return nil
	}
	return &models.Organization{ID: orgID}
}

type capabilityDecisionError struct {
	toolName  string
	operation string
	resource  string
	result    *capability.Result
}

func (e *capabilityDecisionError) Error() string {
	if e == nil || e.result == nil {
		return "capability decision failed"
	}
	return fmt.Sprintf("capability %s required for tool %s (%s): %s", e.result.Effect, e.toolName, e.resource, e.result.Reason)
}

func (e *capabilityDecisionError) requiresApproval() bool {
	return e != nil && e.result != nil && e.result.RequiredApproval
}

func toolOperation(toolName string) (string, bool) {
	switch toolName {
	case "read_file":
		return capability.OpReadFile, true
	case "write_file":
		return capability.OpWriteFile, true
	case "search_files":
		return capability.OpSearchRepo, true
	case "list_directory":
		return capability.OpReadFile, true
	case "apply_patch":
		return capability.OpApplyPatch, true
	case "run_command":
		return capability.OpRunCommand, true
	case "inspect_repo":
		return capability.OpStaticAnalysis, true
	case "get_git_diff":
		return capability.OpReadFile, true
	case "create_commit":
		return capability.OpCreateCommit, true
	case "run_tests":
		return capability.OpRunTests, true
	default:
		return "", false
	}
}

func toolResource(toolName string, input json.RawMessage) string {
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return toolName
	}
	for _, key := range []string{"path", "command", "query"} {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
	}
	return toolName
}

func sandboxState(workspace *models.Workspace) string {
	if workspace == nil {
		return "unknown"
	}
	switch workspace.RuntimeProvider {
	case "docker", "gvisor", "firecracker", "kubernetes":
		return "isolated"
	case "local":
		return "trusted_local"
	default:
		return workspace.RuntimeProvider
	}
}

// executeStep runs a single agent step (alias used by the loop).
func (r *Runner) executeStep(ctx context.Context, step *models.AgentStep, workspacePath string) error {
	output, err := r.executeToolUnchecked(ctx, *step.ToolName, workspacePath, step.ToolInput)
	step.ToolOutput = output
	if err != nil {
		step.Status = models.AgentStepStatusFailed
		errStr := err.Error()
		step.Content = &errStr
		return err
	}
	step.Status = models.AgentStepStatusCompleted
	return nil
}

// checkBudget checks if the run is within budget constraints.
func (r *Runner) checkBudget(ctx context.Context, taskID string, state *RunState) (*budget.CheckResult, error) {
	if r.budget == nil {
		return &budget.CheckResult{Allowed: true}, nil
	}

	// Load task budget if available
	taskBudget, _ := r.loadTaskBudget(ctx, taskID)
	if taskBudget == nil {
		return &budget.CheckResult{Allowed: true}, nil
	}

	rs := &budget.RunState{
		CostSoFar:       state.CostSoFar,
		DurationMinutes: state.DurationMinutes,
		ModelCalls:      state.ModelCalls,
		ToolCalls:       state.ToolCalls,
		ShellCommands:   state.ShellCommands,
		FilesChanged:    state.FilesChanged,
	}

	return r.budget.CheckRun(ctx, taskBudget, rs)
}

// streamStep publishes a step event via NATS.
func (r *Runner) streamStep(ctx context.Context, step *models.AgentStep, eventType string) error {
	if r.eventBus == nil {
		return nil
	}

	payload := map[string]any{
		"run_id":      step.AgentRunID,
		"step_id":     step.ID,
		"step_number": step.StepNumber,
		"tool_name":   step.ToolName,
		"status":      step.Status,
		"type":        eventType,
		"latency_ms":  step.LatencyMs,
		"timestamp":   time.Now().UTC(),
	}
	if step.Content != nil {
		payload["content"] = *step.Content
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("runs.%s.steps", step.AgentRunID)
	return r.eventBus.Publish(subject, data)
}

func (r *Runner) loadRunHistory(ctx context.Context, runID string) []models.AgentStep {
	if r.db == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, agent_run_id, step_number, step_type, status, content,
		       tool_name, tool_input, tool_output, command, command_output,
		       exit_code, file_path, diff, cost, latency_ms, created_at
		FROM agent_steps
		WHERE agent_run_id = $1
		ORDER BY step_number ASC, created_at ASC
	`, runID)
	if err != nil {
		r.logger.Warn("failed to load run history", "run_id", runID, "error", err)
		return nil
	}
	defer rows.Close()

	var history []models.AgentStep
	for rows.Next() {
		var step models.AgentStep
		var content, toolName, toolInput, toolOutput, command, commandOutput, filePath, diff sql.NullString
		var exitCode sql.NullInt32
		if err := rows.Scan(
			&step.ID, &step.AgentRunID, &step.StepNumber, &step.StepType, &step.Status,
			&content, &toolName, &toolInput, &toolOutput, &command, &commandOutput,
			&exitCode, &filePath, &diff, &step.Cost, &step.LatencyMs, &step.CreatedAt,
		); err != nil {
			r.logger.Warn("failed to scan run history step", "run_id", runID, "error", err)
			return history
		}
		if content.Valid {
			step.Content = &content.String
		}
		if toolName.Valid {
			step.ToolName = &toolName.String
		}
		if toolInput.Valid {
			step.ToolInput = json.RawMessage(toolInput.String)
		}
		if toolOutput.Valid {
			step.ToolOutput = json.RawMessage(toolOutput.String)
		}
		if command.Valid {
			step.Command = &command.String
		}
		if commandOutput.Valid {
			step.CommandOutput = &commandOutput.String
		}
		if exitCode.Valid {
			code := int(exitCode.Int32)
			step.ExitCode = &code
		}
		if filePath.Valid {
			step.FilePath = &filePath.String
		}
		if diff.Valid {
			step.Diff = &diff.String
		}
		history = append(history, step)
	}
	if err := rows.Err(); err != nil {
		r.logger.Warn("failed while loading run history", "run_id", runID, "error", err)
	}
	return history
}

func nextStepNumber(history []models.AgentStep) int {
	maxStep := 0
	for _, step := range history {
		if step.StepNumber > maxStep {
			maxStep = step.StepNumber
		}
	}
	return maxStep + 1
}

func seedRunState(run *models.AgentRun, history []models.AgentStep) *RunState {
	state := &RunState{}
	if run != nil {
		state.CostSoFar = run.TotalCost
	}
	for _, step := range history {
		if step.StepType != models.AgentStepTypeToolCall || step.ToolName == nil {
			continue
		}
		state.ToolCalls++
		switch *step.ToolName {
		case "run_command", "run_tests":
			state.ShellCommands++
		case "write_file", "apply_patch":
			state.FilesChanged++
		}
	}
	return state
}

// persistStep saves an agent step to the database.
func (r *Runner) persistStep(ctx context.Context, step *models.AgentStep) error {
	if r.db == nil {
		return nil
	}

	var toolInput, toolOutput sql.NullString
	if step.ToolInput != nil {
		toolInput = sql.NullString{String: string(step.ToolInput), Valid: true}
	}
	if step.ToolOutput != nil {
		toolOutput = sql.NullString{String: string(step.ToolOutput), Valid: true}
	}

	var content, toolName, command, commandOutput, filePath, diff sql.NullString
	if step.Content != nil {
		content = sql.NullString{String: *step.Content, Valid: true}
	}
	if step.ToolName != nil {
		toolName = sql.NullString{String: *step.ToolName, Valid: true}
	}
	if step.Command != nil {
		command = sql.NullString{String: *step.Command, Valid: true}
	}
	if step.CommandOutput != nil {
		commandOutput = sql.NullString{String: *step.CommandOutput, Valid: true}
	}
	if step.FilePath != nil {
		filePath = sql.NullString{String: *step.FilePath, Valid: true}
	}
	if step.Diff != nil {
		diff = sql.NullString{String: *step.Diff, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_steps (
			id, agent_run_id, step_number, step_type, status, content,
			tool_name, tool_input, tool_output, command, command_output,
			exit_code, file_path, diff, cost, latency_ms, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, step.ID, step.AgentRunID, step.StepNumber, step.StepType, step.Status, content,
		toolName, toolInput, toolOutput, command, commandOutput,
		nil, filePath, diff, step.Cost, step.LatencyMs, time.Now().UTC(),
	)
	return err
}

// updateStepStatus updates an existing step in the database.
func (r *Runner) updateStepStatus(ctx context.Context, step *models.AgentStep) error {
	if r.db == nil {
		return nil
	}

	var toolOutput sql.NullString
	if step.ToolOutput != nil {
		toolOutput = sql.NullString{String: string(step.ToolOutput), Valid: true}
	}

	var exitCode sql.NullInt32
	if step.ExitCode != nil {
		exitCode = sql.NullInt32{Int32: int32(*step.ExitCode), Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_steps
		SET status = $1, tool_output = $2, exit_code = $3, latency_ms = $4, content = $5
		WHERE id = $6
	`, step.Status, toolOutput, exitCode, step.LatencyMs, step.Content, step.ID)
	return err
}

// loadAgentRun loads an agent run from the database.
func (r *Runner) loadAgentRun(ctx context.Context, runID string) (*models.AgentRun, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var run models.AgentRun
	var workspaceID, model, provider, errorMessage, summary sql.NullString
	var startedAt, completedAt sql.NullTime
	var metadata sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, task_id, workspace_id, agent_role, model, provider, status,
		       started_at, completed_at, prompt_tokens, completion_tokens,
		       total_cost, error_message, summary, metadata, created_at, updated_at
		FROM agent_runs WHERE id = $1
	`, runID).Scan(
		&run.ID, &run.TaskID, &workspaceID, &run.AgentRole, &model, &provider, &run.Status,
		&startedAt, &completedAt, &run.PromptTokens, &run.CompletionTokens,
		&run.TotalCost, &errorMessage, &summary, &metadata, &run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if workspaceID.Valid {
		run.WorkspaceID = &workspaceID.String
	}
	if model.Valid {
		run.Model = &model.String
	}
	if provider.Valid {
		run.Provider = &provider.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		run.ErrorMessage = &errorMessage.String
	}
	if summary.Valid {
		run.Summary = &summary.String
	}
	if metadata.Valid {
		run.Metadata = json.RawMessage(metadata.String)
	}

	return &run, nil
}

// loadTask loads a task from the database.
func (r *Runner) loadTask(ctx context.Context, taskID string) (*models.Task, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var task models.Task
	var workspaceID, description, sourceID sql.NullString
	var spec, acceptanceCriteria, approvalReqs, metadata sql.NullString
	var maxCost sql.NullFloat64
	var maxRuntime sql.NullInt32
	var startedAt, completedAt, deletedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, `
		SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
		       title, description, status, priority, risk_level, target_branch,
		       spec, acceptance_criteria, max_cost, max_runtime_minutes,
		       approval_requirements, metadata, started_at, completed_at,
		       created_at, updated_at, deleted_at
		FROM tasks WHERE id = $1
	`, taskID).Scan(
		&task.ID, &task.ProjectID, &task.RepositoryID, &workspaceID, &task.CreatedBy, &task.Source, &sourceID,
		&task.Title, &description, &task.Status, &task.Priority, &task.RiskLevel, &task.TargetBranch,
		&spec, &acceptanceCriteria, &maxCost, &maxRuntime,
		&approvalReqs, &metadata, &startedAt, &completedAt,
		&task.CreatedAt, &task.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	if workspaceID.Valid {
		task.WorkspaceID = &workspaceID.String
	}
	if description.Valid {
		task.Description = &description.String
	}
	if sourceID.Valid {
		task.SourceID = &sourceID.String
	}
	if spec.Valid {
		task.Spec = json.RawMessage(spec.String)
	}
	if acceptanceCriteria.Valid {
		task.AcceptanceCriteria = json.RawMessage(acceptanceCriteria.String)
	}
	if maxCost.Valid {
		task.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		task.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if approvalReqs.Valid {
		task.ApprovalRequirements = json.RawMessage(approvalReqs.String)
	}
	if metadata.Valid {
		task.Metadata = json.RawMessage(metadata.String)
	}
	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if deletedAt.Valid {
		task.DeletedAt = &deletedAt.Time
	}

	return &task, nil
}

// loadWorkspace loads a workspace from the database.
func (r *Runner) loadWorkspace(ctx context.Context, workspaceID *string) (*models.Workspace, error) {
	if r.db == nil || workspaceID == nil {
		return nil, fmt.Errorf("workspace ID is nil")
	}

	var ws models.Workspace
	var taskID, worktreePath, runtimeSessionID, previewURL, settings sql.NullString
	var deletedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, `
		SELECT id, repository_id, task_id, name, branch, base_branch, worktree_path,
		       runtime_provider, runtime_session_id, status, preview_url,
		       settings, created_at, updated_at, deleted_at
		FROM workspaces WHERE id = $1
	`, *workspaceID).Scan(
		&ws.ID, &ws.RepositoryID, &taskID, &ws.Name, &ws.Branch, &ws.BaseBranch, &worktreePath,
		&ws.RuntimeProvider, &runtimeSessionID, &ws.Status, &previewURL,
		&settings, &ws.CreatedAt, &ws.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	if taskID.Valid {
		ws.TaskID = &taskID.String
	}
	if worktreePath.Valid {
		ws.WorktreePath = &worktreePath.String
	}
	if runtimeSessionID.Valid {
		ws.RuntimeSessionID = &runtimeSessionID.String
	}
	if previewURL.Valid {
		ws.PreviewURL = &previewURL.String
	}
	if settings.Valid {
		ws.Settings = json.RawMessage(settings.String)
	}
	if deletedAt.Valid {
		ws.DeletedAt = &deletedAt.Time
	}

	return &ws, nil
}

// getWorkspacePath returns the filesystem path for a workspace.
func (r *Runner) getWorkspacePath(ws *models.Workspace) string {
	if ws.WorktreePath != nil && *ws.WorktreePath != "" {
		return *ws.WorktreePath
	}
	// Fallback: construct from workspaces directory
	return filepath.Join("workspaces", ws.ID)
}

// loadTaskBudget loads the budget for a task.
func (r *Runner) loadTaskBudget(ctx context.Context, taskID string) (*models.Budget, error) {
	if r.db == nil {
		return nil, nil
	}

	var b models.Budget
	var projectID, taskIDField sql.NullString
	var maxCost, maxDailySpend sql.NullFloat64
	var maxRuntime, maxModelCalls, maxToolCalls, maxShellCommands, maxConcurrent sql.NullInt32
	var notifications sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, project_id, task_id, type, period,
		       max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
		       max_shell_commands, max_concurrent_agents, max_daily_spend,
		       notifications, created_at, updated_at
		FROM budgets WHERE task_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, taskID).Scan(
		&b.ID, &b.OrganizationID, &projectID, &taskIDField, &b.Type, &b.Period,
		&maxCost, &maxRuntime, &maxModelCalls, &maxToolCalls,
		&maxShellCommands, &maxConcurrent, &maxDailySpend,
		&notifications, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		// Also try project-level budget
		return r.loadProjectBudgetForTask(ctx, taskID)
	}

	if projectID.Valid {
		b.ProjectID = &projectID.String
	}
	if taskIDField.Valid {
		b.TaskID = &taskIDField.String
	}
	if maxCost.Valid {
		b.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		b.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if maxModelCalls.Valid {
		b.MaxModelCalls = int(maxModelCalls.Int32)
	}
	if maxToolCalls.Valid {
		b.MaxToolCalls = int(maxToolCalls.Int32)
	}
	if maxShellCommands.Valid {
		b.MaxShellCommands = int(maxShellCommands.Int32)
	}
	if maxConcurrent.Valid {
		b.MaxConcurrentAgents = int(maxConcurrent.Int32)
	}
	if maxDailySpend.Valid {
		b.MaxDailySpend = &maxDailySpend.Float64
	}
	if notifications.Valid {
		b.Notifications = json.RawMessage(notifications.String)
	}

	return &b, nil
}

// loadProjectBudgetForTask loads the project-level budget for a task.
func (r *Runner) loadProjectBudgetForTask(ctx context.Context, taskID string) (*models.Budget, error) {
	// Get project ID from task
	var projectID string
	err := r.db.QueryRowContext(ctx, `SELECT project_id FROM tasks WHERE id = $1`, taskID).Scan(&projectID)
	if err != nil {
		return nil, err
	}

	var b models.Budget
	var pid, tid sql.NullString
	var maxCost, maxDailySpend sql.NullFloat64
	var maxRuntime, maxModelCalls, maxToolCalls, maxShellCommands, maxConcurrent sql.NullInt32
	var notifications sql.NullString

	err = r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, project_id, task_id, type, period,
		       max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
		       max_shell_commands, max_concurrent_agents, max_daily_spend,
		       notifications, created_at, updated_at
		FROM budgets WHERE project_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, projectID).Scan(
		&b.ID, &b.OrganizationID, &pid, &tid, &b.Type, &b.Period,
		&maxCost, &maxRuntime, &maxModelCalls, &maxToolCalls,
		&maxShellCommands, &maxConcurrent, &maxDailySpend,
		&notifications, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if pid.Valid {
		b.ProjectID = &pid.String
	}
	if tid.Valid {
		b.TaskID = &tid.String
	}
	if maxCost.Valid {
		b.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		b.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if maxModelCalls.Valid {
		b.MaxModelCalls = int(maxModelCalls.Int32)
	}
	if maxToolCalls.Valid {
		b.MaxToolCalls = int(maxToolCalls.Int32)
	}
	if maxShellCommands.Valid {
		b.MaxShellCommands = int(maxShellCommands.Int32)
	}
	if maxConcurrent.Valid {
		b.MaxConcurrentAgents = int(maxConcurrent.Int32)
	}
	if maxDailySpend.Valid {
		b.MaxDailySpend = &maxDailySpend.Float64
	}
	if notifications.Valid {
		b.Notifications = json.RawMessage(notifications.String)
	}

	return &b, nil
}

// updateRunStatus updates the status of an agent run.
func (r *Runner) updateRunStatus(ctx context.Context, runID, status string, summary *string) error {
	if r.db == nil {
		return nil
	}

	now := time.Now().UTC()
	var startedAt interface{}
	if status == models.AgentRunStatusRunning {
		startedAt = now
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = $1, started_at = COALESCE($2, started_at), updated_at = $3
		WHERE id = $4
	`, status, startedAt, now, runID)
	return err
}

// failRun marks a run as failed with an error message.
func (r *Runner) failRun(ctx context.Context, runID string, errorMsg string) error {
	r.logger.Error("agent run failed", "run_id", runID, "error", errorMsg)

	if r.db != nil {
		now := time.Now().UTC()
		_, _ = r.db.ExecContext(ctx, `
			UPDATE agent_runs
			SET status = $1, error_message = $2, completed_at = $3, updated_at = $3
			WHERE id = $4
		`, models.AgentRunStatusFailed, errorMsg, now, runID)
	}

	// Publish run.failed event
	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.failed", runID), map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusFailed,
		"error":     errorMsg,
		"timestamp": time.Now().UTC(),
	})
	_ = r.publishEvent(ctx, events.StreamAgents, events.AgentRunFailed, map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusFailed,
		"error":     errorMsg,
		"timestamp": time.Now().UTC(),
	})

	return fmt.Errorf("run %s failed: %s", runID, errorMsg)
}

func (r *Runner) pauseRun(ctx context.Context, runID string, reason string) error {
	r.logger.Info("agent run paused", "run_id", runID, "reason", reason)

	if r.db != nil {
		now := time.Now().UTC()
		_, _ = r.db.ExecContext(ctx, `
			UPDATE agent_runs
			SET status = $1, error_message = $2, updated_at = $3
			WHERE id = $4
		`, models.AgentRunStatusPaused, reason, now, runID)
	}

	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.paused", runID), map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusPaused,
		"reason":    reason,
		"timestamp": time.Now().UTC(),
	})

	return nil
}

func (r *Runner) requestCapabilityApproval(ctx context.Context, run *models.AgentRun, task *models.Task, decision *capabilityDecisionError) error {
	if r.db == nil || task == nil || decision == nil || decision.result == nil {
		return nil
	}

	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"tool_name":  decision.toolName,
		"operation":  decision.operation,
		"resource":   decision.resource,
		"effect":     decision.result.Effect,
		"risk_level": decision.result.RiskLevel,
		"reason":     decision.result.Reason,
	})
	if err != nil {
		return fmt.Errorf("marshal approval metadata: %w", err)
	}

	var agentRunID any
	if run != nil {
		agentRunID = run.ID
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by, requested_at,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $6, $6)
	`, approvalID, task.ID, agentRunID, "capability:"+decision.operation, task.CreatedBy, now, string(metadata))
	if err != nil {
		return err
	}

	_ = r.publishEvent(ctx, events.StreamRuns, events.ApprovalRequested, map[string]any{
		"approval_id": approvalID,
		"task_id":     task.ID,
		"run_id":      agentRunID,
		"operation":   decision.operation,
		"resource":    decision.resource,
		"timestamp":   now,
	})

	return nil
}

func (r *Runner) requestModelApproval(ctx context.Context, run *models.AgentRun, task *models.Task, reason string) error {
	if r.db == nil || task == nil {
		return nil
	}

	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"source":     "model_request",
		"reason":     reason,
		"agent_role": "",
	})
	if err != nil {
		return fmt.Errorf("marshal approval metadata: %w", err)
	}

	var agentRunID any
	if run != nil {
		agentRunID = run.ID
		var metadataMap map[string]any
		if err := json.Unmarshal(metadata, &metadataMap); err == nil {
			metadataMap["agent_role"] = run.AgentRole
			if encoded, err := json.Marshal(metadataMap); err == nil {
				metadata = encoded
			}
		}
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by, requested_at,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $6, $6)
	`, approvalID, task.ID, agentRunID, models.ApprovalTypeRiskyAction, task.CreatedBy, now, string(metadata))
	if err != nil {
		return err
	}

	_ = r.publishEvent(ctx, events.StreamRuns, events.ApprovalRequested, map[string]any{
		"approval_id":   approvalID,
		"task_id":       task.ID,
		"run_id":        agentRunID,
		"approval_type": models.ApprovalTypeRiskyAction,
		"reason":        reason,
		"timestamp":     now,
	})

	return nil
}

// updateRunCompletion updates the run with final results.
func (r *Runner) updateRunCompletion(ctx context.Context, runID, status, summary string, state *RunState) error {
	if r.db == nil {
		return nil
	}

	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = $1, summary = $2, total_cost = $3,
		    completed_at = $4, updated_at = $4
		WHERE id = $5
	`, status, summary, state.CostSoFar, now, runID)
	return err
}

func (r *Runner) recordModelUsage(ctx context.Context, run *models.AgentRun, task *models.Task, result *modelrouter.CallResult) error {
	if r.db == nil || run == nil || task == nil || result == nil {
		return nil
	}
	model := result.Model
	if model == "" && run.Model != nil {
		model = *run.Model
	}
	provider := result.Provider
	if provider == "" && run.Provider != nil {
		provider = *run.Provider
	}
	if model == "" {
		model = "unknown"
	}
	if provider == "" {
		provider = "unknown"
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO model_usage (
			id, agent_run_id, task_id, model, provider, prompt_tokens,
			completion_tokens, total_tokens, cost, latency_ms, success, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, uuid.New().String(), run.ID, task.ID, model, provider, result.PromptTokens,
		result.CompletionTokens, result.TotalTokens, result.Cost, result.LatencyMs, true, now)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET model = $1,
		    provider = $2,
		    prompt_tokens = COALESCE(prompt_tokens, 0) + $3,
		    completion_tokens = COALESCE(completion_tokens, 0) + $4,
		    total_cost = COALESCE(total_cost, 0) + $5,
		    updated_at = $6
		WHERE id = $7
	`, model, provider, result.PromptTokens, result.CompletionTokens, result.Cost, now, run.ID)
	return err
}

func (r *Runner) loadMailboxMessages(ctx context.Context, taskID, agentRole string, limit int) []models.AgentMessage {
	if r.db == nil || taskID == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata, created_at
		FROM agent_messages
		WHERE task_id = $1 AND (to_agent = $2 OR to_agent = 'broadcast')
		ORDER BY created_at ASC
		LIMIT $3
	`, taskID, agentRole, limit)
	if err != nil {
		r.logger.Warn("failed to load mailbox messages", "task_id", taskID, "agent_role", agentRole, "error", err)
		return nil
	}
	defer rows.Close()

	var messages []models.AgentMessage
	for rows.Next() {
		var id, tid, runID, from, to, messageType, content, metadata sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(&id, &tid, &runID, &from, &to, &messageType, &content, &metadata, &createdAt); err != nil {
			r.logger.Warn("failed to scan mailbox message", "task_id", taskID, "error", err)
			continue
		}
		msg := models.NullAgentMessage(id, tid, runID, from, to, messageType, content, metadata, createdAt)
		if msg != nil {
			messages = append(messages, *msg)
		}
	}
	if err := rows.Err(); err != nil {
		r.logger.Warn("failed to iterate mailbox messages", "task_id", taskID, "error", err)
	}
	return messages
}

func (r *Runner) persistAgentMessage(ctx context.Context, taskID, runID, fromAgent, toAgent, messageType, content string, metadata map[string]any) error {
	if r.db == nil {
		return nil
	}
	if taskID == "" || fromAgent == "" || toAgent == "" || messageType == "" || strings.TrimSpace(content) == "" {
		return fmt.Errorf("agent message missing required fields")
	}
	metadataJSON := "{}"
	if metadata != nil {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal message metadata: %w", err)
		}
		metadataJSON = string(encoded)
	}
	var runIDValue any
	if strings.TrimSpace(runID) != "" {
		runIDValue = runID
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_messages (
			id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.New().String(), taskID, runIDValue, fromAgent, toAgent, messageType, content, metadataJSON, time.Now().UTC())
	return err
}

// publishEvent publishes an event to NATS.
func (r *Runner) publishEvent(ctx context.Context, stream, subject string, payload map[string]any) error {
	if r.eventBus == nil {
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.eventBus.Publish(subject, data)
}

// runFinalChecks executes lint, typecheck, and tests via the test runner.
func (r *Runner) runFinalChecks(ctx context.Context, run *models.AgentRun, task *models.Task, workspace *models.Workspace, workspacePath string) map[string]any {
	results := make(map[string]any)

	// Run tests
	testOutput, testErr := r.executeTool(ctx, run, task, workspace, workspacePath, "run_tests", json.RawMessage(`{}`))
	results["tests"] = map[string]any{
		"output": string(testOutput),
		"error":  fmt.Sprintf("%v", testErr),
	}

	return results
}

// buildSummary creates a human-readable summary of the run.
func (r *Runner) buildSummary(state *RunState, testResults map[string]any, diffOutput json.RawMessage) string {
	summary := fmt.Sprintf(
		"Agent run completed with %d tool calls, %d shell commands, %d files changed in %d minutes.",
		state.ToolCalls, state.ShellCommands, state.FilesChanged, state.DurationMinutes,
	)
	if diffOutput != nil {
		var diffMap map[string]any
		if json.Unmarshal(diffOutput, &diffMap) == nil {
			if files, ok := diffMap["files_changed"].([]any); ok {
				summary += fmt.Sprintf(" Files changed: %d.", len(files))
			}
		}
	}
	return summary
}

// RunState tracks current run state for budget checking.
type RunState struct {
	CostSoFar       float64
	DurationMinutes int
	ModelCalls      int
	ToolCalls       int
	ShellCommands   int
	FilesChanged    int
}
