package capability

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/budget"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

// Kernel is the central authority for all dangerous operations.
// Every operation that could modify state, access secrets, or affect infrastructure
// must pass through the Kernel.
type Kernel struct {
	policyEngine *policies.Engine
	budgetEngine *budget.Engine
	auditLogger  *audit.Logger
	logger       *slog.Logger
}

// Request contains all context needed to evaluate an operation.
type Request struct {
	// Actor
	ActorType string // human, system, agent
	User      *models.User
	AgentRole string // if actor is an agent

	// Scope
	Organization *models.Organization
	Project      *models.Project
	Repository   *models.Repository
	Workspace    *models.Workspace
	Task         *models.Task
	AgentRun     *models.AgentRun

	// Operation
	Operation    string // read_file, write_file, apply_patch, run_command, etc.
	ResourceType string
	Resource     string // specific file path, command, etc.

	// State
	Budget       *models.Budget
	SandboxState string // isolated, trusted_local, etc.

	// Extra context for condition checking
	Details map[string]any
}

// Result is the outcome of a capability evaluation.
type Result struct {
	Effect           policies.Effect
	RequiredApproval bool
	AuditRequired    bool
	Reason           string
	RiskLevel        string
}

// NewKernel creates a new Capability Kernel.
func NewKernel(policyEngine *policies.Engine, budgetEngine *budget.Engine, auditLogger *audit.Logger, logger *slog.Logger) *Kernel {
	if logger == nil {
		logger = slog.Default()
	}
	if policyEngine == nil {
		policyEngine = policies.DefaultEngine()
	}
	if budgetEngine == nil {
		budgetEngine = budget.NewEngine(nil)
	}
	if auditLogger == nil {
		logger.Warn("audit logger not provided; capability checks will not be persisted")
	}
	return &Kernel{
		policyEngine: policyEngine,
		budgetEngine: budgetEngine,
		auditLogger:  auditLogger,
		logger:       logger,
	}
}

// Evaluate checks if the requested operation is permitted.
func (k *Kernel) Evaluate(ctx context.Context, req Request) (*Result, error) {
	// 1. Build the evaluation result
	result := &Result{
		Effect:    policies.EffectAllow,
		RiskLevel: RiskLevelLow,
	}

	// 2. Determine resource type and action from operation if not explicitly set
	resourceType := req.ResourceType
	action := ""
	if resourceType == "" && req.Operation != "" {
		resourceType, action = GetResourceAndAction(req.Operation)
	}
	if resourceType == "" {
		resourceType = "unknown"
	}

	// 3. Check RBAC permissions
	if req.User != nil {
		hasRBAC := policies.Can(req.User, resourceType, action)
		if !hasRBAC {
			result.Effect = policies.EffectDeny
			result.RequiredApproval = false
			result.Reason = fmt.Sprintf("RBAC: user %s with role %s lacks permission for %s:%s",
				req.User.ID, req.User.Role, resourceType, action)
			result.RiskLevel = RiskLevelHigh
			k.logAudit(ctx, req, result)
			return result, nil
		}
	}

	// 4. Evaluate policies
	policyReq := policies.EvaluationRequest{
		User:         req.User,
		Task:         req.Task,
		Workspace:    req.Workspace,
		Action:       action,
		ResourceType: resourceType,
		Resource:     req.Resource,
		Details:      req.Details,
	}

	effect, err := k.policyEngine.Evaluate(ctx, policyReq, policies.EffectAsk)
	if err != nil {
		k.logger.Error("policy evaluation failed", "error", err, "operation", req.Operation)
		result.Effect = policies.EffectDeny
		result.Reason = fmt.Sprintf("policy evaluation error: %v", err)
		result.RiskLevel = RiskLevelCritical
		return result, err
	}
	result.Effect = effect

	// 5. Check budget constraints if budget is provided
	if req.Budget != nil && !req.Budget.IsUnlimited() && k.budgetEngine != nil {
		runState := k.buildRunState(req)
		checkResult, err := k.budgetEngine.CheckRun(ctx, req.Budget, runState)
		if err != nil {
			k.logger.Error("budget check failed", "error", err)
		}
		if checkResult != nil && !checkResult.Allowed {
			result.Effect = policies.EffectDeny
			result.Reason = fmt.Sprintf("budget constraint violated: %s", checkResult.Reason)
			result.RiskLevel = RiskLevelHigh
			k.logAudit(ctx, req, result)
			return result, nil
		}
	}

	// 6. Check sandbox state
	if req.SandboxState == "isolated" && isHighRiskOperation(req.Operation) {
		if effect == policies.EffectAllow {
			// Upgrade to ask in isolated environments for high-risk ops
			effect = policies.EffectAsk
			result.Effect = effect
		}
	}

	// 7. Determine approval requirement
	result.RequiredApproval = effect == policies.EffectAsk || effect == policies.EffectAdminOnly

	// 8. Determine risk level
	result.RiskLevel = k.calculateRiskLevel(req, effect)

	// 9. Build reason string
	result.Reason = k.buildReason(req, result)

	// 10. Audit logging
	result.AuditRequired = isAuditedOperation(req.Operation) || effect == policies.EffectDeny || effect == policies.EffectAdminOnly
	k.logAudit(ctx, req, result)

	return result, nil
}

// EvaluateBatch evaluates multiple operations and returns the most restrictive result.
func (k *Kernel) EvaluateBatch(ctx context.Context, req Request, operations []string) (*Result, error) {
	overallResult := &Result{
		Effect:    policies.EffectAllow,
		RiskLevel: RiskLevelLow,
	}

	for _, op := range operations {
		req.Operation = op
		result, err := k.Evaluate(ctx, req)
		if err != nil {
			return nil, err
		}
		if result.Effect.MoreRestrictiveThan(overallResult.Effect) {
			overallResult.Effect = result.Effect
			overallResult.RequiredApproval = result.RequiredApproval
			overallResult.Reason = result.Reason
			overallResult.RiskLevel = result.RiskLevel
		}
		if result.Effect == policies.EffectDeny {
			// Short-circuit on deny
			break
		}
	}

	return overallResult, nil
}

// IsAdminOnly returns true if the user must be an admin/owner for this operation.
func (k *Kernel) IsAdminOnly(operation string) bool {
	// Admin-only operations
	switch operation {
	case OpDeploy, OpMergePR, OpWriteSecret, OpRotateSecret, OpModifyPolicy, OpModifyBudget,
		OpDestructiveDB, OpDeleteWorkspace:
		return true
	}
	return false
}

// buildRunState constructs a RunState from the request for budget checking.
func (k *Kernel) buildRunState(req Request) *budget.RunState {
	rs := &budget.RunState{}
	if req.AgentRun != nil {
		rs.CostSoFar = req.AgentRun.TotalCost
		rs.ModelCalls = req.AgentRun.PromptTokens/1000 + req.AgentRun.CompletionTokens/1000
		if req.AgentRun.StartedAt != nil && req.AgentRun.CompletedAt != nil {
			rs.DurationMinutes = int(req.AgentRun.CompletedAt.Sub(*req.AgentRun.StartedAt).Minutes())
		}
	}
	if req.Details != nil {
		if v, ok := req.Details["tool_calls"]; ok {
			rs.ToolCalls, _ = v.(int)
		}
		if v, ok := req.Details["shell_commands"]; ok {
			rs.ShellCommands, _ = v.(int)
		}
		if v, ok := req.Details["files_changed"]; ok {
			rs.FilesChanged, _ = v.(int)
		}
		if v, ok := req.Details["diff_size_kb"]; ok {
			rs.DiffSizeKB, _ = v.(int)
		}
	}
	return rs
}

// calculateRiskLevel determines the risk level for an operation.
func (k *Kernel) calculateRiskLevel(req Request, effect policies.Effect) string {
	switch {
	case effect == policies.EffectAdminOnly:
		return RiskLevelCritical
	case effect == policies.EffectDeny:
		return RiskLevelHigh
	case isHighRiskOperation(req.Operation):
		return RiskLevelHigh
	case effect == policies.EffectAsk:
		return RiskLevelMedium
	case req.Operation == OpAccessSecret || req.Operation == OpWriteSecret || req.Operation == OpNetworkRequest:
		return RiskLevelMedium
	default:
		return RiskLevelLow
	}
}

// buildReason creates a human-readable reason string.
func (k *Kernel) buildReason(req Request, result *Result) string {
	switch result.Effect {
	case policies.EffectAllow:
		return fmt.Sprintf("allowed: %s on %s", req.Operation, req.Resource)
	case policies.EffectAsk:
		return fmt.Sprintf("requires approval: %s on %s (%s risk)", req.Operation, req.Resource, result.RiskLevel)
	case policies.EffectDeny:
		if result.Reason != "" {
			return result.Reason
		}
		return fmt.Sprintf("denied: %s on %s", req.Operation, req.Resource)
	case policies.EffectAdminOnly:
		return fmt.Sprintf("admin only: %s on %s requires elevated privileges", req.Operation, req.Resource)
	default:
		return fmt.Sprintf("unknown effect for %s", req.Operation)
	}
}

// logAudit writes an audit record for the capability check.
func (k *Kernel) logAudit(ctx context.Context, req Request, result *Result) {
	if k.auditLogger == nil {
		return
	}

	orgID := ""
	if req.Organization != nil {
		orgID = req.Organization.ID
	}
	if orgID == "" && req.User != nil {
		orgID = req.User.OrganizationID
	}
	if orgID == "" && req.Details != nil {
		if value, ok := req.Details["organization_id"].(string); ok {
			orgID = value
		}
	}
	actorType := req.ActorType
	if actorType == "" {
		actorType = "system"
	}
	actorID := ""
	if req.User != nil {
		actorID = req.User.ID
		if actorType == "system" {
			actorType = "human"
		}
	}
	if actorID == "" && req.AgentRun != nil {
		actorID = req.AgentRun.ID
		if actorType == "system" {
			actorType = "agent"
		}
	}

	err := k.auditLogger.LogCapabilityCheckForActor(ctx, orgID, actorType, actorID, req.Operation, req.Resource, result.Effect.String(), result.Effect != policies.EffectDeny, result.Reason)
	if err != nil {
		k.logger.Error("failed to write capability audit log", "error", err)
	}
}

// Risk level constants.
const (
	RiskLevelLow      = "low"
	RiskLevelMedium   = "medium"
	RiskLevelHigh     = "high"
	RiskLevelCritical = "critical"
)

// isHighRiskOperation returns true for operations that are inherently dangerous.
func isHighRiskOperation(op string) bool {
	switch op {
	case OpDeploy, OpDestructiveDB, OpMergePR, OpDeleteWorkspace,
		OpModifyPolicy, OpModifyBudget, OpPushBranch, OpWriteSecret, OpRotateSecret:
		return true
	}
	return false
}

// isAuditedOperation returns true for operations that must always be audited.
func isAuditedOperation(op string) bool {
	switch op {
	case OpDeploy, OpMergePR, OpAccessSecret, OpWriteSecret, OpModifyPolicy, OpModifyBudget,
		OpRotateSecret, OpDestructiveDB, OpDeleteWorkspace:
		return true
	}
	return false
}
