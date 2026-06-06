package agents

import (
	"context"
	"encoding/json"
)

// Tool represents a capability that an agent can invoke.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Handler     ToolHandler     `json:"-"`
}

// ToolHandler is the function signature for tool implementations.
type ToolHandler func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

// StandardTools is the default set of tools available to agents.
var StandardTools = []Tool{
	{
		Name:        "read_file",
		Description: "Read the contents of a file at a given path. Returns the file content as a string.",
	},
	{
		Name:        "write_file",
		Description: "Write content to a file at a given path. Creates the file if it does not exist, overwrites if it does.",
	},
	{
		Name:        "search_files",
		Description: "Search for patterns across files in the workspace using ripgrep. Returns matching lines with file paths and line numbers.",
	},
	{
		Name:        "apply_patch",
		Description: "Apply a unified diff patch to files in the workspace. The patch should be in standard unified diff format.",
	},
	{
		Name:        "run_command",
		Description: "Run a shell command in the workspace. Returns stdout, stderr, and exit code. Use with caution.",
	},
	{
		Name:        "list_directory",
		Description: "List the contents of a directory. Returns file and subdirectory names with basic metadata.",
	},
	{
		Name:        "inspect_repo",
		Description: "Get repository structure and metadata including file tree, language breakdown, and key files.",
	},
	{
		Name:        "get_git_diff",
		Description: "Get the git diff of current changes in the workspace. Returns the diff as a string.",
	},
	{
		Name:        "create_commit",
		Description: "Stage all changes and create a git commit with the given message.",
	},
	{
		Name:        "run_tests",
		Description: "Run the test suite for the project. Returns test output and pass/fail status.",
	},
}

// ToolRegistry provides lookup and registration of tools.
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates a registry pre-populated with StandardTools.
func NewToolRegistry() *ToolRegistry {
	r := &ToolRegistry{
		tools: make(map[string]Tool, len(StandardTools)),
	}
	for _, t := range StandardTools {
		r.tools[t.Name] = t
	}
	return r
}

// Register adds a tool to the registry. Returns error if the tool already exists.
func (r *ToolRegistry) Register(tool Tool) error {
	if _, exists := r.tools[tool.Name]; exists {
		return ToolAlreadyExistsError{Name: tool.Name}
	}
	r.tools[tool.Name] = tool
	return nil
}

// Get retrieves a tool by name. Returns ok=false if not found.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *ToolRegistry) List() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToolAlreadyExistsError is returned when registering a duplicate tool.
type ToolAlreadyExistsError struct {
	Name string
}

func (e ToolAlreadyExistsError) Error() string {
	return "tool already registered: " + e.Name
}

// ToolNotFoundError is returned when looking up a non-existent tool.
type ToolNotFoundError struct {
	Name string
}

func (e ToolNotFoundError) Error() string {
	return "tool not found: " + e.Name
}
