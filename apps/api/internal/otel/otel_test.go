package otel

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

func TestSetup(t *testing.T) {
	providers, cleanup, err := Setup(context.Background(), "test-service", "1.0.0", slog.Default())
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	if providers == nil {
		t.Fatal("expected non-nil Providers")
	}
	if providers.TracerProvider == nil {
		t.Fatal("expected non-nil TracerProvider")
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup function")
	}

	// Run cleanup to flush and shut down
	cleanup()
}

func TestTracer(t *testing.T) {
	_, cleanup, err := Setup(context.Background(), "test-service", "1.0.0", slog.Default())
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	defer cleanup()

	tracer := Tracer()
	if tracer == nil {
		t.Fatal("expected non-nil Tracer")
	}
}

func TestEnvOrDefault_Unset(t *testing.T) {
	os.Unsetenv("TEST_OTEL_UNSET_VAR")
	val := config.EnvOrDefault("TEST_OTEL_UNSET_VAR", "default-val")
	if val != "default-val" {
		t.Errorf("expected 'default-val', got %q", val)
	}
}

func TestEnvOrDefault_Set(t *testing.T) {
	os.Setenv("TEST_OTEL_SET_VAR", "custom-val")
	defer os.Unsetenv("TEST_OTEL_SET_VAR")

	val := config.EnvOrDefault("TEST_OTEL_SET_VAR", "default-val")
	if val != "custom-val" {
		t.Errorf("expected 'custom-val', got %q", val)
	}
}
