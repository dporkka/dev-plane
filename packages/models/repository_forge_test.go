package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRepositoryForgeSettingsDefaultsToGitHub(t *testing.T) {
	settings, err := ParseRepositoryForgeSettings(nil)
	if err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if settings.Provider != ForgeProviderGitHub {
		t.Fatalf("provider = %q, want %q", settings.Provider, ForgeProviderGitHub)
	}
	if settings.BaseURL != "" {
		t.Fatalf("base url = %q, want empty", settings.BaseURL)
	}
}

func TestParseRepositoryForgeSettingsReadsGiteaConfig(t *testing.T) {
	raw := json.RawMessage(`{"other":{"keep":true},"forge":{"provider":"gitea","base_url":"https://git.example.test/root/"}}`)
	settings, err := ParseRepositoryForgeSettings(raw)
	if err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if settings.Provider != ForgeProviderGitea {
		t.Fatalf("provider = %q, want %q", settings.Provider, ForgeProviderGitea)
	}
	if settings.BaseURL != "https://git.example.test/root" {
		t.Fatalf("base url = %q", settings.BaseURL)
	}
}

func TestParseRepositoryForgeSettingsRejectsUnknownProvider(t *testing.T) {
	_, err := ParseRepositoryForgeSettings(json.RawMessage(`{"forge":{"provider":"mystery"}}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported forge provider") {
		t.Fatalf("error = %v, want unsupported provider", err)
	}
}

func TestParseRepositoryForgeSettingsRequiresGiteaBaseURL(t *testing.T) {
	_, err := ParseRepositoryForgeSettings(json.RawMessage(`{"forge":{"provider":"gitea"}}`))
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("error = %v, want base_url validation", err)
	}
}

func TestParseRepositoryForgeSettingsRejectsCredentialBearingBaseURL(t *testing.T) {
	_, err := ParseRepositoryForgeSettings(json.RawMessage(`{"forge":{"provider":"gitea","base_url":"https://user:pass@git.example.test"}}`))
	if err == nil || !strings.Contains(err.Error(), "userinfo") {
		t.Fatalf("error = %v, want userinfo validation", err)
	}
}

func TestPutRepositoryForgeSettingsPreservesUnrelatedSettings(t *testing.T) {
	raw := json.RawMessage(`{"runtime":{"provider":"nulang"},"feature":true}`)
	updated, err := PutRepositoryForgeSettings(raw, RepositoryForgeSettings{
		Provider: ForgeProviderGitea,
		BaseURL:  "https://git.example.test/",
	})
	if err != nil {
		t.Fatalf("put settings: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if decoded["feature"] != true {
		t.Fatalf("feature setting was not preserved: %#v", decoded)
	}
	runtime, ok := decoded["runtime"].(map[string]any)
	if !ok || runtime["provider"] != "nulang" {
		t.Fatalf("runtime setting was not preserved: %#v", decoded["runtime"])
	}
	forge, ok := decoded["forge"].(map[string]any)
	if !ok || forge["provider"] != "gitea" || forge["base_url"] != "https://git.example.test" {
		t.Fatalf("forge settings = %#v", decoded["forge"])
	}
}
