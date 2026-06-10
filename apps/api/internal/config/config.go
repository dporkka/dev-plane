// Package config provides application configuration loaded from environment variables.
package config

import (
	"os"
	"strings"
)

// Config holds all application configuration settings.
type Config struct {
	Port                string
	DatabaseURL         string
	JWTSecret           string
	GitHubClientID      string
	GitHubSecret        string
	GitHubWebhookSecret string
	NATSURL             string
	AllowedOrigins      []string
	LogLevel            string
	AgentVaultURL       string
	AgentVaultToken     string
	AgentVaultProject   string
	SecretKeys          string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:                getEnv("PORT", "8080"),
		DatabaseURL:         getEnv("DATABASE_URL", "file:./data/dev.db?_journal_mode=WAL"),
		JWTSecret:           getEnv("JWT_SECRET", "dev-secret-change-in-production"),
		GitHubClientID:      getEnv("GITHUB_CLIENT_ID", ""),
		GitHubSecret:        getEnv("GITHUB_CLIENT_SECRET", ""),
		GitHubWebhookSecret: getEnv("GITHUB_APP_WEBHOOK_SECRET", ""),
		NATSURL:             getEnv("NATS_URL", "nats://localhost:4222"),
		AllowedOrigins:      getOrigins(),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
		AgentVaultURL:       getEnv("AGENTVAULT_URL", ""),
		AgentVaultToken:     getEnv("AGENTVAULT_TOKEN", ""),
		AgentVaultProject:   getEnv("AGENTVAULT_PROJECT", "dev-plane"),
		SecretKeys:          getEnv("SECRET_ENCRYPTION_KEYS", ""),
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
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
