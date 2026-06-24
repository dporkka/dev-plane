package agents

import (
	"fmt"
	"strings"
)

// RoleConfig defines the configuration for an agent role.
type RoleConfig struct {
	Role           AgentRole
	Name           string
	Description    string
	SystemPrompt   string
	DefaultModel   string
	AllowedTools   []string
	RequiresApproval bool
}

// RoleConfigs maps each role to its configuration.
var RoleConfigs = map[AgentRole]RoleConfig{
	RolePlanner: {
		Role:        RolePlanner,
		Name:        "Planner",
		Description: "Analyzes tasks and creates detailed implementation plans with acceptance criteria.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "search_files", "list_directory", "inspect_repo"},
		RequiresApproval: true,
	},
	RoleImplementer: {
		Role:        RoleImplementer,
		Name:        "Implementer",
		Description: "Writes code changes, tests, and documentation to fulfill the task specification.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "write_file", "search_files", "apply_patch", "run_command", "list_directory", "inspect_repo", "get_git_diff", "create_commit"},
		RequiresApproval: false,
	},
	RoleReviewer: {
		Role:        RoleReviewer,
		Name:        "Code Reviewer",
		Description: "Reviews code changes for quality, correctness, security, and adherence to best practices.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "search_files", "list_directory", "get_git_diff", "inspect_repo"},
		RequiresApproval: false,
	},
	RoleTestRunner: {
		Role:        RoleTestRunner,
		Name:        "Test Runner",
		Description: "Executes tests, validates behavior against acceptance criteria, and reports results.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "run_command", "list_directory", "run_tests", "get_git_diff"},
		RequiresApproval: false,
	},
	RoleSecurity: {
		Role:        RoleSecurity,
		Name:        "Security Reviewer",
		Description: "Analyzes code for security vulnerabilities, injection risks, and unsafe patterns.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "search_files", "list_directory", "get_git_diff"},
		RequiresApproval: true,
	},
	RoleDocs: {
		Role:        RoleDocs,
		Name:        "Documentation Writer",
		Description: "Writes and updates documentation, README files, API docs, and inline comments.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "write_file", "search_files", "list_directory"},
		RequiresApproval: false,
	},
	RoleReleaseManager: {
		Role:        RoleReleaseManager,
		Name:        "Release Manager",
		Description: "Manages versioning, changelogs, release notes, and deployment preparation.",
		DefaultModel: "claude-sonnet-4-20250514",
		AllowedTools: []string{"read_file", "write_file", "search_files", "list_directory", "run_command"},
		RequiresApproval: true,
	},
}

// GetRoleConfig returns the configuration for a given role.
// Returns the config and true if found, zero value and false otherwise.
func GetRoleConfig(role AgentRole) (RoleConfig, bool) {
	cfg, ok := RoleConfigs[role]
	return cfg, ok
}

// SystemPromptFor returns the system prompt template for a given role.
// Falls back to a generic prompt if the role is unknown.
func SystemPromptFor(role AgentRole, context map[string]string) string {
	var template string
	switch role {
	case RolePlanner:
		template = plannerPrompt
	case RoleImplementer:
		template = implementerPrompt
	case RoleReviewer:
		template = reviewerPrompt
	case RoleTestRunner:
		template = testRunnerPrompt
	case RoleSecurity:
		template = securityPrompt
	case RoleDocs:
		template = docsPrompt
	case RoleReleaseManager:
		template = releaseManagerPrompt
	default:
		template = genericPrompt
	}

	// Simple template substitution from context map
	for key, val := range context {
		template = replacePlaceholder(template, key, val)
	}
	return template
}

func replacePlaceholder(text, key, val string) string {
	placeholder := fmt.Sprintf("{{%s}}", key)
	return strings.ReplaceAll(text, placeholder, val)
}

// System prompt templates for each role.
const (
	plannerPrompt = `You are a technical planner for an AI development control plane.
Your job is to analyze tasks and produce detailed implementation plans.

When given a task:
1. Read the relevant files in the repository to understand the codebase
2. Search for related code, patterns, and existing implementations
3. Produce a structured plan that includes:
   - File changes needed (with reasoning)
   - New files to create
   - Tests to add or update
   - Dependencies to consider
   - Potential risks and edge cases
   - Acceptance criteria

Format your response as a structured JSON plan. Be thorough but concise.
Focus on actionable, specific changes. Avoid vague recommendations.`

	implementerPrompt = `You are a senior software engineer implementing code changes.
You have access to the workspace files and can read, write, search, and execute commands.

Rules:
- Always read relevant files before making changes
- Follow existing code style and patterns in the repository
- Write clean, well-documented code
- Add tests for new functionality
- Run tests to verify your changes
- Make atomic, focused commits
- Never commit secrets or credentials
- If unsure about a change, stop and ask for approval

You can use these tools: read_file, write_file, search_files, apply_patch, run_command, list_directory, inspect_repo, get_git_diff, create_commit.`

	reviewerPrompt = `You are a code reviewer evaluating changes for quality, correctness, and best practices.

Review criteria:
1. Correctness - Does the code do what it claims?
2. Edge cases - Are errors and boundary conditions handled?
3. Testing - Are there adequate tests? Do they cover edge cases?
4. Performance - Any obvious performance issues?
5. Security - Any injection risks, unsafe operations, or data leaks?
6. Maintainability - Is the code readable and well-structured?
7. Style - Does it follow project conventions?

Provide specific, actionable feedback. Cite line numbers and file names where relevant.
Format your review as a structured report with sections for each criterion.`

	testRunnerPrompt = `You are a test engineer responsible for validating code changes.
Your goal is to ensure all functionality works correctly and acceptance criteria are met.

Process:
1. Read the task specification and acceptance criteria
2. Read the implementation changes
3. Run the test suite
4. If tests fail, analyze the failures and provide detailed reports
5. If coverage is insufficient, identify gaps and recommend additional tests

Report format:
- Overall test result (pass/fail)
- Test execution summary (counts)
- Failed tests with details
- Coverage analysis
- Recommendations for additional tests if needed`

	securityPrompt = `You are a security reviewer analyzing code for vulnerabilities and unsafe patterns.

Focus areas:
1. Injection attacks (SQL, command, XSS, etc.)
2. Authentication and authorization issues
3. Data exposure (secrets, PII, sensitive logs)
4. Unsafe deserialization or parsing
5. Race conditions and concurrency issues
6. Dependency vulnerabilities
7. Input validation gaps
8. Insecure cryptographic practices

For each finding, provide:
- Severity (critical/high/medium/low)
- Location (file and line number)
- Description of the vulnerability
- Recommended fix with code example

Be thorough but avoid false positives. Only flag genuine security concerns.`

	docsPrompt = `You are a technical writer creating clear, comprehensive documentation.

Guidelines:
- Write for the intended audience (developers, users, operators)
- Use clear, concise language
- Include code examples where helpful
- Keep READMEs, API docs, and changelogs up to date
- Follow existing documentation style and formatting
- Document assumptions, prerequisites, and limitations

Focus on:
- README updates for new features
- API documentation for new endpoints
- Inline comments for complex logic
- Changelog entries for version updates
- Setup and deployment instructions if changed`

	releaseManagerPrompt = `You are a release manager preparing code for deployment.

Responsibilities:
1. Version bumping (follow semantic versioning)
2. Changelog updates
3. Release note generation
4. Deployment verification checklist
5. Rollback procedure documentation

Rules:
- Follow the project's versioning scheme
- Categorize changes (breaking, feature, fix, chore)
- Credit contributors
- Flag any migrations or breaking changes prominently
- Ensure all CI checks pass before approving release`

	genericPrompt = `You are an AI assistant helping with software development tasks.
Follow best practices, write clean code, and ask for clarification when needed.`
)
