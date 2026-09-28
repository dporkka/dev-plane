package capability

import "github.com/ai-dev-control-plane/models"

// All operations that must go through the Capability Kernel.
const (
	// File operations
	OpReadFile   = "read_file"
	OpWriteFile  = "write_file"
	OpApplyPatch = "apply_patch"
	OpDeleteFile = "delete_file"

	// Command operations
	OpRunCommand = "run_command"
	OpRunTests   = "run_tests"
	OpInstallDep = "install_dependency"

	// Secret operations
	OpAccessSecret = "access_secret"
	OpWriteSecret  = "write_secret"
	OpRotateSecret = "rotate_secret"

	// Network operations
	OpNetworkRequest = "network_request"

	// MCP / tool operations
	OpCallMCPTool = "call_mcp_tool"

	// Preview / runtime operations
	OpStartPreview = "start_preview_server"
	OpStopPreview  = "stop_preview_server"

	// Database operations
	OpRunMigration  = "run_migration"
	OpDestructiveDB = "destructive_db"

	// Git / forge operations
	OpReadRepository = "read_repository"
	OpCreateBranch   = "create_branch"
	OpCreateCommit   = "create_commit"
	OpPushBranch     = "push_branch"
	OpOpenPR         = "open_pull_request"
	OpReviewPR       = "review_pull_request"
	OpMergePR        = "merge_pull_request"
	OpReadChecks     = "read_checks"

	// Deploy operations
	OpDeploy = "deploy"

	// Administrative operations
	OpDeleteWorkspace = "delete_workspace"
	OpModifyPolicy    = "modify_policy"
	OpModifyBudget    = "modify_budget"
	OpSearchRepo      = "search_repository"
	OpStaticAnalysis  = "static_analysis"
)

// operationResourceMap maps each operation to its resource type and action
// for policy engine evaluation.
var operationResourceMap = map[string]struct {
	ResourceType string
	Action       string
}{
	OpReadFile:        {ResourceType: "file", Action: "read"},
	OpWriteFile:       {ResourceType: "file", Action: "write"},
	OpApplyPatch:      {ResourceType: "file", Action: "write"},
	OpDeleteFile:      {ResourceType: "file", Action: "delete"},
	OpRunCommand:      {ResourceType: "command", Action: "execute"},
	OpRunTests:        {ResourceType: "command", Action: "test"},
	OpInstallDep:      {ResourceType: "command", Action: "install"},
	OpAccessSecret:    {ResourceType: "secret", Action: "read"},
	OpWriteSecret:     {ResourceType: "secret", Action: "write"},
	OpRotateSecret:    {ResourceType: "secret", Action: "rotate"},
	OpNetworkRequest:  {ResourceType: "network", Action: "request"},
	OpCallMCPTool:     {ResourceType: "command", Action: "execute"},
	OpStartPreview:    {ResourceType: "deploy", Action: "execute"},
	OpStopPreview:     {ResourceType: "deploy", Action: "execute"},
	OpRunMigration:    {ResourceType: "command", Action: "migrate"},
	OpDestructiveDB:   {ResourceType: "command", Action: "destructive_db"},
	OpReadRepository:  {ResourceType: "git", Action: "read"},
	OpCreateBranch:    {ResourceType: "git", Action: "create_branch"},
	OpCreateCommit:    {ResourceType: "git", Action: "commit"},
	OpPushBranch:      {ResourceType: "git", Action: "push"},
	OpOpenPR:          {ResourceType: "git", Action: "create_pr"},
	OpReviewPR:        {ResourceType: "git", Action: "review"},
	OpMergePR:         {ResourceType: "git", Action: "merge"},
	OpReadChecks:      {ResourceType: "git", Action: "read_checks"},
	OpDeploy:          {ResourceType: "deploy", Action: "execute"},
	OpDeleteWorkspace: {ResourceType: "workspace", Action: "delete"},
	OpModifyPolicy:    {ResourceType: "policy", Action: "write"},
	OpModifyBudget:    {ResourceType: "budget", Action: "write"},
	OpSearchRepo:      {ResourceType: "command", Action: "search"},
	OpStaticAnalysis:  {ResourceType: "command", Action: "analyze"},
}

// GetResourceType returns the resource type for a given operation.
func GetResourceType(op string) string {
	if m, ok := operationResourceMap[op]; ok {
		return m.ResourceType
	}
	return ""
}

// GetAction returns the action for a given operation.
func GetAction(op string) string {
	if m, ok := operationResourceMap[op]; ok {
		return m.Action
	}
	return ""
}

// GetResourceAndAction returns both resource type and action for a given operation.
func GetResourceAndAction(op string) (resourceType, action string) {
	if m, ok := operationResourceMap[op]; ok {
		return m.ResourceType, m.Action
	}
	return "", ""
}

// ForgeOperation* values are the provider-neutral operation vocabulary shared
// with Nulang's forge gateway. They deliberately describe intent rather than a
// specific forge provider API.
const (
	ForgeOperationRepoRead     = "repo.read"
	ForgeOperationBranchCreate = "branch.create"
	ForgeOperationCommitWrite  = "commit.write"
	ForgeOperationChangeCreate = "change.create"
	ForgeOperationChangeReview = "change.review"
	ForgeOperationChangeMerge  = "change.merge"
	ForgeOperationCheckRead    = "check.read"
)

var forgeOperationMap = map[string]string{
	ForgeOperationRepoRead:     OpReadRepository,
	ForgeOperationBranchCreate: OpCreateBranch,
	ForgeOperationCommitWrite:  OpCreateCommit,
	ForgeOperationChangeCreate: OpOpenPR,
	ForgeOperationChangeReview: OpReviewPR,
	ForgeOperationChangeMerge:  OpMergePR,
	ForgeOperationCheckRead:    OpReadChecks,
}

// OperationForForge maps the provider-neutral forge vocabulary onto the
// capability kernel's canonical operations. Unknown operations fail closed.
func OperationForForge(operation string) (string, bool) {
	op, ok := forgeOperationMap[operation]
	return op, ok
}


var forgeAgentRoleOperations = map[string]map[string]struct{}{
	models.AgentRolePlanner: {
		ForgeOperationRepoRead: {},
		ForgeOperationCheckRead: {},
	},
	models.AgentRoleImplementer: {
		ForgeOperationRepoRead:     {},
		ForgeOperationBranchCreate: {},
		ForgeOperationCommitWrite:  {},
		ForgeOperationChangeCreate: {},
		ForgeOperationCheckRead:    {},
	},
	models.AgentRoleReviewer: {
		ForgeOperationRepoRead:      {},
		ForgeOperationChangeReview: {},
		ForgeOperationCheckRead:     {},
	},
	models.AgentRoleTestRunner: {
		ForgeOperationRepoRead:  {},
		ForgeOperationCheckRead: {},
	},
	models.AgentRoleSecurity: {
		ForgeOperationRepoRead:      {},
		ForgeOperationChangeReview: {},
		ForgeOperationCheckRead:     {},
	},
	models.AgentRoleDocs: {
		ForgeOperationRepoRead:     {},
		ForgeOperationBranchCreate: {},
		ForgeOperationCommitWrite:  {},
		ForgeOperationChangeCreate: {},
		ForgeOperationCheckRead:    {},
	},
	models.AgentRoleReleaseManager: {
		ForgeOperationRepoRead:      {},
		ForgeOperationChangeReview: {},
		ForgeOperationChangeMerge:  {},
		ForgeOperationCheckRead:     {},
	},
}

// ForgeOperationAllowedForAgentRole enforces least-privilege forge authority
// independently of the caller's local session grants. Unknown roles fail closed.
func ForgeOperationAllowedForAgentRole(role, operation string) bool {
	operations, ok := forgeAgentRoleOperations[role]
	if !ok {
		return false
	}
	_, ok = operations[operation]
	return ok
}
