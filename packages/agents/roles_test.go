package agents

import "testing"

func TestGetRoleConfig(t *testing.T) {
	roles := []AgentRole{
		RolePlanner,
		RoleImplementer,
		RoleReviewer,
		RoleTestRunner,
		RoleSecurity,
		RoleDocs,
		RoleReleaseManager,
	}

	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			cfg, ok := GetRoleConfig(role)
			assertTrue(t, ok, "config should be found")
			assertEqual(t, cfg.Role, role)
			assertTrue(t, cfg.Name != "", "name should not be empty")
			assertTrue(t, cfg.Description != "", "description should not be empty")
			assertTrue(t, cfg.DefaultModel != "", "default_model should not be empty")
		})
	}
}

func TestGetRoleConfig_Unknown(t *testing.T) {
	cfg, ok := GetRoleConfig("unknown_role")
	assertEqual(t, ok, false)
	assertEqual(t, cfg.Role, AgentRole(""))
}

func TestRoleConfigs_Count(t *testing.T) {
	assertEqual(t, len(RoleConfigs), 7)
}

func TestSystemPromptFor(t *testing.T) {
	roles := []AgentRole{
		RolePlanner,
		RoleImplementer,
		RoleReviewer,
		RoleTestRunner,
		RoleSecurity,
		RoleDocs,
		RoleReleaseManager,
	}

	context := map[string]string{"task": "test task"}

	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			prompt := SystemPromptFor(role, context)
			assertTrue(t, prompt != "", "prompt should not be empty")
		})
	}
}

func TestSystemPromptFor_Unknown(t *testing.T) {
	prompt := SystemPromptFor("unknown_role", nil)
	assertTrue(t, prompt != "", "should return generic prompt")
	// Generic prompt should contain a generic fallback message
	assertTrue(t, len(prompt) > 0, "generic prompt should have content")
}
