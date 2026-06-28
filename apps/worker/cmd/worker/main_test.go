package main

import (
	"os"
	"testing"
)

func TestInitRuntimeProvider_RejectsLocalInProduction(t *testing.T) {
	os.Setenv("APP_ENV", "production")
	defer os.Unsetenv("APP_ENV")

	_, _, err := initRuntimeProvider("local", "/tmp/workspaces", "", "")
	if err == nil {
		t.Fatal("expected error for local runtime in production")
	}
}

func TestInitRuntimeProvider_AllowsLocalInDevelopment(t *testing.T) {
	os.Unsetenv("APP_ENV")
	os.Unsetenv("ENV")

	_, _, err := initRuntimeProvider("local", "/tmp/workspaces", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
