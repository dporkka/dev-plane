package models

import "testing"

func TestPlan_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{PlanFree, "free"},
		{PlanPro, "pro"},
		{PlanEnterprise, "enterprise"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestOrganization_Validate(t *testing.T) {
	t.Run("valid organization", func(t *testing.T) {
		o := &Organization{
			Name: "Acme Corp",
			Slug: "acme-corp",
		}
		assertError(t, o.Validate(), false)
	})

	t.Run("missing name", func(t *testing.T) {
		o := &Organization{
			Slug: "acme-corp",
		}
		err := o.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "organization name is required" {
			t.Errorf("got %q, want %q", err.Error(), "organization name is required")
		}
	})

	t.Run("missing slug", func(t *testing.T) {
		o := &Organization{
			Name: "Acme Corp",
		}
		err := o.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "organization slug is required" {
			t.Errorf("got %q, want %q", err.Error(), "organization slug is required")
		}
	})
}
