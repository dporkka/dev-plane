package config

import "testing"

func TestLoadReadsGiteaWebhookSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("GITEA_WEBHOOK_SECRET", " gitea-secret ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.GiteaWebhookSecret != "gitea-secret" {
		t.Fatalf("GiteaWebhookSecret = %q, want gitea-secret", cfg.GiteaWebhookSecret)
	}
}
