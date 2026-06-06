package models

import "testing"

func TestRole_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{RoleOwner, "owner"},
		{RoleAdmin, "admin"},
		{RoleMember, "member"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestUser_Validate(t *testing.T) {
	t.Run("valid user", func(t *testing.T) {
		u := &User{
			Email:          "test@example.com",
			OrganizationID: "org-1",
			Role:           RoleMember,
		}
		assertError(t, u.Validate(), false)
	})

	t.Run("missing email", func(t *testing.T) {
		u := &User{
			OrganizationID: "org-1",
			Role:           RoleMember,
		}
		err := u.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "user email is required" {
			t.Errorf("got %q, want %q", err.Error(), "user email is required")
		}
	})

	t.Run("missing organization_id", func(t *testing.T) {
		u := &User{
			Email: "test@example.com",
			Role:  RoleMember,
		}
		err := u.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "user organization_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "user organization_id is required")
		}
	})

	t.Run("invalid role", func(t *testing.T) {
		u := &User{
			Email:          "test@example.com",
			OrganizationID: "org-1",
			Role:           "invalid_role",
		}
		err := u.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "invalid user role" {
			t.Errorf("got %q, want %q", err.Error(), "invalid user role")
		}
	})
}
