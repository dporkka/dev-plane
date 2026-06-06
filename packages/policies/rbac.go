package policies

import (
	"github.com/ai-dev-control-plane/models"
)

// RoleHierarchy defines the inheritance chain for roles.
// Each role inherits permissions from roles lower in the hierarchy.
var RoleHierarchy = []string{
	models.RoleMember,
	models.RoleAdmin,
	models.RoleOwner,
}

// RoleRank maps each role to its numeric rank (higher = more privileges).
var RoleRank = map[string]int{
	models.RoleMember: 0,
	models.RoleAdmin:  1,
	models.RoleOwner:  2,
}

// Permission represents a specific action on a resource type.
type Permission struct {
	ResourceType string `json:"resource_type"`
	Action       string `json:"action"`
}

// RBACConfig defines role-based permissions for the system.
var RBACConfig = map[string][]Permission{
	models.RoleOwner: {
		{ResourceType: "*", Action: "*"}, // Full access
	},
	models.RoleAdmin: {
		{ResourceType: models.ResourceTypeFile, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeFile, Action: models.ActionWrite},
		{ResourceType: models.ResourceTypeCommand, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeCommand, Action: models.ActionExecute},
		{ResourceType: models.ResourceTypeGit, Action: "*"},
		{ResourceType: models.ResourceTypeNetwork, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeDeploy, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeDeploy, Action: models.ActionExecute},
		{ResourceType: models.ResourceTypeSecret, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeSecret, Action: models.ActionWrite},
		{ResourceType: "organization", Action: models.ActionRead},
		{ResourceType: "organization", Action: models.ActionWrite},
		{ResourceType: "project", Action: "*"},
		{ResourceType: "user", Action: "*"},
		{ResourceType: "policy", Action: "*"},
		{ResourceType: "audit", Action: models.ActionRead},
		{ResourceType: "integration", Action: "*"},
	},
	models.RoleMember: {
		{ResourceType: models.ResourceTypeFile, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeFile, Action: models.ActionWrite},
		{ResourceType: models.ResourceTypeCommand, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeGit, Action: models.ActionRead},
		{ResourceType: models.ResourceTypeGit, Action: models.ActionWrite},
		{ResourceType: models.ResourceTypeNetwork, Action: models.ActionRead},
		{ResourceType: "project", Action: models.ActionRead},
		{ResourceType: "task", Action: "*"},
		{ResourceType: "repository", Action: models.ActionRead},
	},
}

// HasRole checks if a user has at least the minimum required role.
// Returns true if the user's role rank is >= the minimum role rank.
func HasRole(user *models.User, minRole string) bool {
	if user == nil {
		return false
	}
	userRank, userOk := RoleRank[user.Role]
	minRank, minOk := RoleRank[minRole]
	if !userOk || !minOk {
		return false
	}
	return userRank >= minRank
}

// Can checks if a user has a specific permission.
func Can(user *models.User, resourceType, action string) bool {
	if user == nil {
		return false
	}

	perms, ok := RBACConfig[user.Role]
	if !ok {
		return false
	}

	for _, p := range perms {
		// Wildcard resource type
		if p.ResourceType == "*" && (p.Action == "*" || p.Action == action) {
			return true
		}
		// Wildcard action
		if p.ResourceType == resourceType && (p.Action == "*" || p.Action == action) {
			return true
		}
		// Exact match
		if p.ResourceType == resourceType && p.Action == action {
			return true
		}
	}

	return false
}

// CanAny checks if a user has any of the given permissions.
// Returns true on the first matching permission.
func CanAny(user *models.User, perms []Permission) bool {
	for _, p := range perms {
		if Can(user, p.ResourceType, p.Action) {
			return true
		}
	}
	return false
}

// CanAll checks if a user has all of the given permissions.
func CanAll(user *models.User, perms []Permission) bool {
	for _, p := range perms {
		if !Can(user, p.ResourceType, p.Action) {
			return false
		}
	}
	return true
}

// IsOwner returns true if the user is an organization owner.
func IsOwner(user *models.User) bool {
	return user != nil && user.Role == models.RoleOwner
}

// IsAdmin returns true if the user is an admin or owner.
func IsAdmin(user *models.User) bool {
	return HasRole(user, models.RoleAdmin)
}

// GetPermissionsForRole returns all permissions assigned to a role.
func GetPermissionsForRole(role string) []Permission {
	perms, ok := RBACConfig[role]
	if !ok {
		return nil
	}
	result := make([]Permission, len(perms))
	copy(result, perms)
	return result
}

// RoleNames returns all defined role names.
func RoleNames() []string {
	return []string{
		models.RoleOwner,
		models.RoleAdmin,
		models.RoleMember,
	}
}
