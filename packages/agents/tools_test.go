package agents

import (
	"testing"
)

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

func TestNewToolRegistry(t *testing.T) {
	reg := NewToolRegistry()
	assertTrue(t, reg != nil, "registry should not be nil")

	// Should be pre-populated with standard tools
	tool, ok := reg.Get("read_file")
	assertTrue(t, ok, "read_file tool should exist")
	assertEqual(t, tool.Name, "read_file")
}

func TestToolRegistry_Get(t *testing.T) {
	reg := NewToolRegistry()

	t.Run("retrieves existing tool", func(t *testing.T) {
		tool, ok := reg.Get("write_file")
		assertTrue(t, ok, "write_file should be found")
		assertEqual(t, tool.Name, "write_file")
		assertTrue(t, tool.Description != "", "description should not be empty")
	})

	t.Run("returns false for unknown tool", func(t *testing.T) {
		_, ok := reg.Get("nonexistent_tool")
		assertEqual(t, ok, false)
	})
}

func TestToolRegistry_Get_NotFound(t *testing.T) {
	reg := NewToolRegistry()
	_, ok := reg.Get("unknown_tool_xyz")
	assertEqual(t, ok, false)
}

func TestToolRegistry_List(t *testing.T) {
	reg := NewToolRegistry()
	tools := reg.List()
	assertEqual(t, len(tools), 10)

	// Verify all expected tools are present
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expected := []string{
		"read_file", "write_file", "search_files", "apply_patch",
		"run_command", "list_directory", "inspect_repo", "get_git_diff",
		"create_commit", "run_tests",
	}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("expected tool %q not found in list", name)
		}
	}
}

func TestToolRegistry_Register(t *testing.T) {
	reg := NewToolRegistry()

	newTool := Tool{
		Name:        "custom_tool",
		Description: "A custom tool for testing",
	}

	err := reg.Register(newTool)
	assertEqual(t, err, nil)

	// Verify it was added
	 retrieved, ok := reg.Get("custom_tool")
	assertTrue(t, ok, "custom_tool should be registered")
	assertEqual(t, retrieved.Name, "custom_tool")
}

func TestToolRegistry_Register_Duplicate(t *testing.T) {
	reg := NewToolRegistry()

	dupTool := Tool{
		Name:        "read_file",
		Description: "Duplicate tool",
	}

	err := reg.Register(dupTool)
	assertTrue(t, err != nil, "should return error for duplicate")

	// Verify the error type
	if _, ok := err.(ToolAlreadyExistsError); !ok {
		t.Errorf("expected ToolAlreadyExistsError, got %T", err)
	}
}

func TestStandardTools_Count(t *testing.T) {
	assertEqual(t, len(StandardTools), 10)
}
