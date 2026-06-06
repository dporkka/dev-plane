package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/models"
)

// BuildSystemPrompt creates the system prompt for an agent run.
// Includes: role, task description, spec, available tools, workspace context.
func BuildSystemPrompt(role string, task *models.Task, workspace *models.Workspace) string {
	var b strings.Builder

	// Header
	b.WriteString("You are a senior software engineering agent.\n")
	b.WriteString(fmt.Sprintf("Your role: %s\n", role))
	b.WriteString("You have access to tools that let you read files, write files, " +
		"search code, run commands, apply patches, and interact with git.\n\n")

	// Available tools description
	b.WriteString("## Available Tools\n\n")
	b.WriteString("1. read_file - Read the contents of a file.\n")
	b.WriteString("   Input: {\"path\": \"path/to/file\"}\n\n")
	b.WriteString("2. write_file - Write content to a file.\n")
	b.WriteString("   Input: {\"path\": \"path/to/file\", \"content\": \"...\"}\n\n")
	b.WriteString("3. search_files - Search for patterns across files.\n")
	b.WriteString("   Input: {\"query\": \"search term\", \"path\": \".\", \"glob\": \"*.go\"}\n\n")
	b.WriteString("4. list_directory - List directory contents.\n")
	b.WriteString("   Input: {\"path\": \".\"}\n\n")
	b.WriteString("5. apply_patch - Apply a unified diff patch.\n")
	b.WriteString("   Input: {\"patch\": \"--- a/file\\n+++ b/file\\n...\"}\n\n")
	b.WriteString("6. run_command - Execute a shell command.\n")
	b.WriteString("   Input: {\"command\": \"go test ./...\", \"timeout\": 120}\n\n")
	b.WriteString("7. inspect_repo - Get repository structure and metadata.\n")
	b.WriteString("   Input: {}\n\n")
	b.WriteString("8. get_git_diff - Get current uncommitted changes.\n")
	b.WriteString("   Input: {}\n\n")
	b.WriteString("9. create_commit - Stage all changes and create a commit.\n")
	b.WriteString("   Input: {\"message\": \"feat: description\"}\n\n")
	b.WriteString("10. run_tests - Run the project's test suite.\n")
	b.WriteString("   Input: {\"command\": \"go test ./...\", \"timeout\": 300}\n\n")

	// Task context
	b.WriteString("## Task\n\n")
	b.WriteString(fmt.Sprintf("Title: %s\n", task.Title))
	if task.Description != nil && *task.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", *task.Description))
	}
	b.WriteString(fmt.Sprintf("Priority: %s\n", task.Priority))
	b.WriteString(fmt.Sprintf("Risk Level: %s\n", task.RiskLevel))
	b.WriteString(fmt.Sprintf("Target Branch: %s\n", task.TargetBranch))

	// Spec
	if task.Spec != nil && len(task.Spec) > 0 {
		b.WriteString("\n## Spec\n\n")
		var specMap map[string]any
		if err := json.Unmarshal(task.Spec, &specMap); err == nil {
			b.WriteString(formatMap(specMap, 0))
		} else {
			b.WriteString(string(task.Spec))
		}
		b.WriteString("\n")
	}

	// Acceptance criteria
	if task.AcceptanceCriteria != nil && len(task.AcceptanceCriteria) > 0 {
		b.WriteString("\n## Acceptance Criteria\n\n")
		var criteria []string
		if err := json.Unmarshal(task.AcceptanceCriteria, &criteria); err == nil {
			for i, c := range criteria {
				b.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
			}
		} else {
			b.WriteString(string(task.AcceptanceCriteria))
		}
		b.WriteString("\n")
	}

	// Workspace context
	b.WriteString("\n## Workspace\n\n")
	b.WriteString(fmt.Sprintf("Workspace ID: %s\n", workspace.ID))
	b.WriteString(fmt.Sprintf("Branch: %s\n", workspace.Branch))
	if workspace.BaseBranch != "" {
		b.WriteString(fmt.Sprintf("Base Branch: %s\n", workspace.BaseBranch))
	}

	// Role-specific instructions
	b.WriteString("\n")
	b.WriteString(buildRoleInstructions(role))

	// Rules
	b.WriteString("\n## Rules\n\n")
	b.WriteString("1. Always inspect the repository structure before making changes.\n")
	b.WriteString("2. Search for existing code patterns before writing new code.\n")
	b.WriteString("3. Read relevant files before modifying them.\n")
	b.WriteString("4. Run tests after making changes.\n")
	b.WriteString("5. Follow existing code style and conventions.\n")
	b.WriteString("6. Create focused, atomic commits.\n")
	b.WriteString("7. Never commit secrets or sensitive data.\n")
	b.WriteString("8. Prefer small, reviewable changes over large diffs.\n")

	return b.String()
}

// BuildToolCallPrompt creates a prompt asking the model to choose the next
// structured action for the agent loop.
func BuildToolCallPrompt(ctx context.Context, history []models.AgentStep, mailbox []models.AgentMessage, task *models.Task) string {
	_ = ctx
	var b strings.Builder

	b.WriteString("## Previous Steps\n\n")
	if len(history) == 0 {
		b.WriteString("No previous steps. Starting fresh.\n")
	} else {
		for _, step := range history {
			b.WriteString(fmt.Sprintf("Step %d: %s", step.StepNumber, step.StepType))
			if step.ToolName != nil {
				b.WriteString(fmt.Sprintf(" (%s)", *step.ToolName))
			}
			b.WriteString(fmt.Sprintf(" - %s\n", step.Status))
			if step.Content != nil && *step.Content != "" {
				b.WriteString(fmt.Sprintf("Content: %s\n", truncateForPrompt(*step.Content, 1200)))
			}
			if len(step.ToolOutput) > 0 {
				b.WriteString(fmt.Sprintf("Tool output: %s\n", truncateForPrompt(string(step.ToolOutput), 2000)))
			}
		}
	}

	b.WriteString("\n## Mailbox Messages\n\n")
	if len(mailbox) == 0 {
		b.WriteString("No mailbox messages for this role.\n")
	} else {
		for _, msg := range mailbox {
			b.WriteString(fmt.Sprintf("From %s (%s): %s\n", msg.FromAgent, msg.MessageType, truncateForPrompt(msg.Content, 2000)))
		}
	}

	b.WriteString("\n## Instructions\n\n")
	b.WriteString("Decide the next single action for the task. Return only valid JSON with one of these shapes:\n")
	b.WriteString(`{"action":"tool_call","tool_name":"inspect_repo","tool_input":{}}` + "\n")
	b.WriteString(`{"action":"final_response","content":"Short completion summary"}` + "\n")
	b.WriteString(`{"action":"handoff","to_agent":"reviewer","message_type":"handoff","content":"Context for the next role","metadata":{}}` + "\n")
	b.WriteString(`{"action":"request_approval","content":"Why human approval is needed"}` + "\n\n")
	b.WriteString("Use tool_call while more repository inspection, edits, or verification are needed. Use handoff when this role has produced durable context for another role. Use final_response only when this role is complete.\n")

	return b.String()
}

func truncateForPrompt(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	if maxLen <= 20 {
		return value[:maxLen]
	}
	return value[:maxLen-15] + "\n...[truncated]"
}

// buildRoleInstructions returns role-specific instructions.
func buildRoleInstructions(role string) string {
	switch role {
	case models.AgentRoleImplementer:
		return "## Role Instructions (Implementer)\n\n" +
			"You implement features and fix bugs. Your job is to:\n" +
			"1. Understand the existing codebase\n" +
			"2. Implement the requested changes\n" +
			"3. Ensure tests pass\n" +
			"4. Follow the project's coding conventions\n\n" +
			"When implementing:\n" +
			"- Search for similar patterns first\n" +
			"- Keep changes minimal and focused\n" +
			"- Update tests as needed\n" +
			"- Run go fmt and go vet before committing\n"

	case models.AgentRolePlanner:
		return "## Role Instructions (Planner)\n\n" +
			"You create implementation plans. Analyze the codebase and create\n" +
			"a step-by-step plan for implementing the requested feature.\n"

	case models.AgentRoleReviewer:
		return "## Role Instructions (Reviewer)\n\n" +
			"You review code changes. Analyze diffs and provide feedback on:\n" +
			"- Code quality\n" +
			"- Potential bugs\n" +
			"- Security issues\n" +
			"- Adherence to conventions\n"

	case models.AgentRoleTestRunner:
		return "## Role Instructions (Test Runner)\n\n" +
			"You focus on testing. Run tests, analyze coverage, and\n" +
			"create or update tests as needed.\n"

	case models.AgentRoleSecurity:
		return "## Role Instructions (Security Reviewer)\n\n" +
			"You focus on security. Review code for:\n" +
			"- Injection vulnerabilities\n" +
			"- Insecure dependencies\n" +
			"- Secret exposure\n" +
			"- Access control issues\n"

	case models.AgentRoleDocs:
		return "## Role Instructions (Docs Writer)\n\n" +
			"You write documentation. Update READMEs, comments, and\n" +
			"documentation files to reflect changes.\n"

	case models.AgentRoleReleaseManager:
		return "## Role Instructions (Release Manager)\n\n" +
			"You manage releases. Create changelogs, version bumps,\n" +
			"and release notes.\n"

	default:
		return fmt.Sprintf("## Role Instructions (%s)\n\nExecute the task using available tools.\n", role)
	}
}

// formatMap formats a map as indented text.
func formatMap(m map[string]any, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			b.WriteString(fmt.Sprintf("%s%s:\n", prefix, k))
			b.WriteString(formatMap(val, indent+1))
		case []any:
			b.WriteString(fmt.Sprintf("%s%s:\n", prefix, k))
			for _, item := range val {
				b.WriteString(fmt.Sprintf("%s  - %v\n", prefix, item))
			}
		default:
			b.WriteString(fmt.Sprintf("%s%s: %v\n", prefix, k, v))
		}
	}
	return b.String()
}
