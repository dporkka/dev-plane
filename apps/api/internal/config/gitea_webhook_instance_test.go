package config

import "testing"

func TestGiteaWebhookConfigurationPreservesSecretSuffix(t *testing.T) {
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("GITEA_URL", "https://git.example.test/code/")
	t.Setenv("GITEA_WEBHOOK_SECRET", "secret-with-slash/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GiteaWebhookURL != "https://git.example.test/code/" || cfg.GiteaWebhookSecret != "secret-with-slash/" {
		t.Fatal("Gitea instance or signing key was changed")
	}
}
