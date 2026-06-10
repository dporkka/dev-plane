package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	integrationTypeGitHub  = "github"
	integrationTypeLinear  = "linear"
	integrationTypeSlack   = "slack"
	integrationTypeDiscord = "discord"
	integrationTypeWebhook = "webhook"
	integrationTypeVoice   = "voice"
)

type IntegrationProvider struct {
	Type                 string   `json:"type"`
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	Capabilities         []string `json:"capabilities"`
	RequiredConfigFields []string `json:"required_config_fields"`
	SupportsWebhook      bool     `json:"supports_webhook"`
	SupportsCommands     bool     `json:"supports_commands"`
	SupportsVoice        bool     `json:"supports_voice"`
}

type integrationRuntimeConfig struct {
	ProjectID        string `json:"project_id"`
	RepositoryID     string `json:"repository_id"`
	CreatedBy        string `json:"created_by"`
	WebhookSecret    string `json:"webhook_secret"`
	CommandSecret    string `json:"command_secret"`
	VoiceProvider    string `json:"voice_provider"`
	DefaultPriority  string `json:"default_priority"`
	DefaultRiskLevel string `json:"default_risk_level"`
	TargetBranch     string `json:"target_branch"`
}

func SupportedIntegrationProviders() []IntegrationProvider {
	return []IntegrationProvider{
		{
			Type:                 integrationTypeGitHub,
			Name:                 "GitHub",
			Description:          "Repository sync, pull requests, and signed GitHub webhooks.",
			Capabilities:         []string{"repositories", "pull_requests", "webhooks"},
			RequiredConfigFields: []string{"project_id", "repository_id", "created_by"},
			SupportsWebhook:      true,
		},
		{
			Type:                 integrationTypeLinear,
			Name:                 "Linear",
			Description:          "Issue sync via signed webhooks and task status updates.",
			Capabilities:         []string{"ticket_sync", "webhooks"},
			RequiredConfigFields: []string{"project_id", "repository_id", "created_by", "webhook_secret"},
			SupportsWebhook:      true,
		},
		{
			Type:                 integrationTypeSlack,
			Name:                 "Slack",
			Description:          "Slash-style task commands over signed webhook requests.",
			Capabilities:         []string{"commands", "approvals", "webhooks"},
			RequiredConfigFields: []string{"project_id", "repository_id", "created_by", "webhook_secret"},
			SupportsWebhook:      true,
			SupportsCommands:     true,
		},
		{
			Type:                 integrationTypeDiscord,
			Name:                 "Discord",
			Description:          "Command-driven task workflows using Discord-style webhook payloads.",
			Capabilities:         []string{"commands", "approvals", "webhooks"},
			RequiredConfigFields: []string{"project_id", "repository_id", "created_by", "webhook_secret"},
			SupportsWebhook:      true,
			SupportsCommands:     true,
		},
		{
			Type:                 integrationTypeWebhook,
			Name:                 "Generic Webhook",
			Description:          "Provider-agnostic webhook ingestion for custom automation sources.",
			Capabilities:         []string{"webhooks", "task_creation"},
			RequiredConfigFields: []string{"project_id", "repository_id", "created_by", "webhook_secret"},
			SupportsWebhook:      true,
		},
		{
			Type:                 integrationTypeVoice,
			Name:                 "Voice / Whisper",
			Description:          "Voice task intake using Whisper-compatible transcript metadata.",
			Capabilities:         []string{"voice_input", "task_creation"},
			RequiredConfigFields: []string{"project_id", "repository_id", "created_by"},
			SupportsVoice:        true,
		},
	}
}

func supportedIntegrationType(t string) bool {
	_, ok := integrationProviderByType(t)
	return ok
}

func integrationProviderByType(t string) (IntegrationProvider, bool) {
	for _, provider := range SupportedIntegrationProviders() {
		if provider.Type == t {
			return provider, true
		}
	}
	return IntegrationProvider{}, false
}

func integrationWebhookPath(providerType, integrationID string) string {
	switch providerType {
	case integrationTypeGitHub:
		return "/api/v1/webhooks/github"
	case integrationTypeVoice:
		return ""
	default:
		return fmt.Sprintf("/api/v1/webhooks/%s/%s", providerType, integrationID)
	}
}

func parseIntegrationConfig(raw json.RawMessage) (integrationRuntimeConfig, error) {
	if len(raw) == 0 {
		return integrationRuntimeConfig{}, nil
	}
	var cfg integrationRuntimeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return integrationRuntimeConfig{}, err
	}
	return cfg, nil
}

func integrationSourceForProvider(providerType string) string {
	switch providerType {
	case integrationTypeLinear:
		return integrationTypeLinear
	case integrationTypeSlack:
		return integrationTypeSlack
	case integrationTypeDiscord:
		return integrationTypeDiscord
	case integrationTypeVoice:
		return integrationTypeVoice
	case integrationTypeWebhook:
		return integrationTypeWebhook
	default:
		return "web"
	}
}

func summarizeTranscript(transcript string) string {
	trimmed := strings.TrimSpace(transcript)
	if trimmed == "" {
		return ""
	}
	if idx := strings.IndexAny(trimmed, ".!?\n"); idx > 0 {
		trimmed = trimmed[:idx]
	}
	trimmed = strings.TrimSpace(trimmed)
	if len(trimmed) > 72 {
		trimmed = strings.TrimSpace(trimmed[:72])
	}
	if trimmed == "" {
		return "Voice task"
	}
	return trimmed
}
