package agents

import "testing"

func TestAgentRole_Valid(t *testing.T) {
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
			assertTrue(t, role.Valid(), string(role)+" should be valid")
		})
	}
}

func TestAgentRole_Invalid(t *testing.T) {
	unknown := AgentRole("unknown_role")
	assertEqual(t, unknown.Valid(), false)
}

func TestAgentRole_String(t *testing.T) {
	tests := []struct {
		role AgentRole
		want string
	}{
		{RolePlanner, "planner"},
		{RoleImplementer, "implementer"},
		{RoleReviewer, "reviewer"},
		{RoleTestRunner, "test_runner"},
		{RoleSecurity, "security_reviewer"},
		{RoleDocs, "docs_writer"},
		{RoleReleaseManager, "release_manager"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.role.String(), tt.want)
		})
	}
}
