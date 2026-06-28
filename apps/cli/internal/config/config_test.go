package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSave(t *testing.T) {
	tmp := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", originalHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load empty config: %v", err)
	}
	if cfg.BaseURL != "" {
		t.Fatalf("expected empty base url, got %q", cfg.BaseURL)
	}

	cfg.BaseURL = "http://localhost:8080"
	cfg.Token = "secret-token"
	if err := Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	path, _ := Path()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if loaded.BaseURL != cfg.BaseURL || loaded.Token != cfg.Token {
		t.Fatalf("config mismatch: got %+v", loaded)
	}

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config dir is too permissive: %o", info.Mode().Perm())
	}
}
