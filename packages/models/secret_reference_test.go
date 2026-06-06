package models

import (
	"testing"
	"time"
)

func TestSecretScope_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{SecretScopeDev, "dev"},
		{SecretScopeStaging, "staging"},
		{SecretScopeProd, "prod"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestSecretProvider_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{SecretProviderSOPS, "sops"},
		{SecretProviderEnv, "env"},
		{SecretProviderVault, "vault"},
		{SecretProviderEncryptedDB, "encrypted_db"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestSecretReference_ShouldRotate(t *testing.T) {
	t.Run("should rotate when never rotated", func(t *testing.T) {
		s := &SecretReference{LastRotatedAt: nil}
		assertEqual(t, s.ShouldRotate(24*time.Hour), true)
	})

	t.Run("should rotate when past rotation period", func(t *testing.T) {
		past := time.Now().Add(-48 * time.Hour)
		s := &SecretReference{LastRotatedAt: &past}
		assertEqual(t, s.ShouldRotate(24*time.Hour), true)
	})

	t.Run("should not rotate when within rotation period", func(t *testing.T) {
		recent := time.Now().Add(-1 * time.Hour)
		s := &SecretReference{LastRotatedAt: &recent}
		assertEqual(t, s.ShouldRotate(24*time.Hour), false)
	})
}

func TestSecretReference_Validate(t *testing.T) {
	t.Run("valid secret reference", func(t *testing.T) {
		s := &SecretReference{
			OrganizationID: "org-1",
			Name:           "api-key",
			Scope:          SecretScopeDev,
			Provider:       SecretProviderEnv,
			KeyPath:        "API_KEY",
		}
		assertError(t, s.Validate(), false)
	})

	t.Run("missing organization_id", func(t *testing.T) {
		s := &SecretReference{
			Name:     "api-key",
			Scope:    SecretScopeDev,
			Provider: SecretProviderEnv,
			KeyPath:  "API_KEY",
		}
		err := s.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "secret_reference organization_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "secret_reference organization_id is required")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		s := &SecretReference{
			OrganizationID: "org-1",
			Scope:          SecretScopeDev,
			Provider:       SecretProviderEnv,
			KeyPath:        "API_KEY",
		}
		err := s.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "secret_reference name is required" {
			t.Errorf("got %q, want %q", err.Error(), "secret_reference name is required")
		}
	})

	t.Run("missing scope", func(t *testing.T) {
		s := &SecretReference{
			OrganizationID: "org-1",
			Name:           "api-key",
			Provider:       SecretProviderEnv,
			KeyPath:        "API_KEY",
		}
		err := s.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "secret_reference scope is required" {
			t.Errorf("got %q, want %q", err.Error(), "secret_reference scope is required")
		}
	})

	t.Run("missing provider", func(t *testing.T) {
		s := &SecretReference{
			OrganizationID: "org-1",
			Name:           "api-key",
			Scope:          SecretScopeDev,
			KeyPath:        "API_KEY",
		}
		err := s.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "secret_reference provider is required" {
			t.Errorf("got %q, want %q", err.Error(), "secret_reference provider is required")
		}
	})

	t.Run("missing key_path", func(t *testing.T) {
		s := &SecretReference{
			OrganizationID: "org-1",
			Name:           "api-key",
			Scope:          SecretScopeDev,
			Provider:       SecretProviderEnv,
		}
		err := s.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "secret_reference key_path is required" {
			t.Errorf("got %q, want %q", err.Error(), "secret_reference key_path is required")
		}
	})
}
