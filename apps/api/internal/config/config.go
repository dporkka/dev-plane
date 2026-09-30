// Package config provides application configuration loaded from environment variables.
package config

import (
	"errors"
	"os"
	"strings"
)

// Config holds all application configuration settings.
type Config struct {
	Port                  string
	DatabaseURL           string
	JWTSecret             string
	GitHubClientID        string
	GitHubSecret          string
	GitHubToken           string
	GitHubWebhookSecret   string
	ForgeProvider         string
	ForgeGitRemote        string
	GiteaURL              string
	GiteaToken            string
	GiteaUsername         string
	GiteaDraftTitlePrefix string
	LinearWebhookSecret   string
	SlackSigningSecret    string
	DiscordWebhookSecret  string
	NATSURL               string
	AllowedOrigins        []string
	OAuthCookieSecure     bool
	LogLevel              string
	AgentVaultURL         string
	AgentVaultToken       string
	AgentVaultProject     string
	SecretKeys            string
}

// Load reads configuration from environment variables with sensible defaults.
// It returns an error if required security settings are missing or too weak.
func Load() (*Config, error) {
	jwtSecret := EnvOrDefault("JWT_SECRET", "")
	if len(jwtSecret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}

	return &Config{
		Port:                  EnvOrDefault("PORT", "8080"),
		DatabaseURL:           EnvOrDefault("DATABASE_URL", "file:./data/dev.db?_journal_mode=WAL"),
		JWTSecret:             jwtSecret,
		GitHubClientID:        EnvOrDefault("GITHUB_CLIENT_ID", ""),
		GitHubSecret:          EnvOrDefault("GITHUB_CLIENT_SECRET", ""),
		GitHubToken:           EnvValueOrDefault("GITHUB_TOKEN", ""),
		GitHubWebhookSecret:   EnvOrDefault("GITHUB_APP_WEBHOOK_SECRET", ""),
		ForgeProvider:         strings.ToLower(EnvValueOrDefault("FORGE_PROVIDER", "github")),
		ForgeGitRemote:        EnvValueOrDefault("FORGE_GIT_REMOTE", "origin"),
		GiteaURL:              EnvOrDefault("GITEA_URL", ""),
		GiteaToken:            EnvValueOrDefault("GITEA_TOKEN", ""),
		GiteaUsername:         EnvValueOrDefault("GITEA_USERNAME", ""),
		GiteaDraftTitlePrefix: EnvValueOrDefault("GITEA_DRAFT_TITLE_PREFIX", "WIP:"),
		LinearWebhookSecret:   EnvOrDefault("LINEAR_WEBHOOK_SECRET", ""),
		SlackSigningSecret:    EnvOrDefault("SLACK_SIGNING_SECRET", ""),
		DiscordWebhookSecret:  EnvOrDefault("DISCORD_WEBHOOK_SECRET", ""),
		NATSURL:               EnvOrDefault("NATS_URL", "nats://localhost:4222"),
		AllowedOrigins:        getOrigins(),
		OAuthCookieSecure:     EnvOrDefault("OAUTH_COOKIE_SECURE", "true") == "true",
		LogLevel:              EnvOrDefault("LOG_LEVEL", "info"),
		AgentVaultURL:         EnvOrDefault("AGENTVAULT_URL", ""),
		AgentVaultToken:       EnvOrDefault("AGENTVAULT_TOKEN", ""),
		AgentVaultProject:     EnvOrDefault("AGENTVAULT_PROJECT", "dev-plane"),
		SecretKeys:            EnvOrDefault("SECRET_ENCRYPTION_KEYS", ""),
	}, nil
}

// EnvValueOrDefault reads and trims an environment value without changing its
// internal or trailing characters. Use it for credentials, names, and other
// non-URL configuration.
func EnvValueOrDefault(key, defaultVal string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return defaultVal
}

// EnvOrDefault reads an environment variable, returning the default if unset or empty.
// Trailing slashes are trimmed to normalize URL values.
func EnvOrDefault(key, defaultVal string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultVal
}

func getOrigins() []string {
	v := os.Getenv("ALLOWED_ORIGINS")
	if v == "" {
		return []string{"http://localhost:3000"}
	}
	return strings.Split(v, ",")
}
