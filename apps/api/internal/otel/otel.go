// Package otel provides OpenTelemetry instrumentation for the API service.
//
// It initializes tracer and meter providers, provides HTTP middleware for
// request tracing, and wraps database operations with spans.
package otel

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// instrumentationName is the name used to identify this instrumentation.
	instrumentationName = "github.com/ai-dev-control-plane/api/internal/otel"
)

// Providers holds initialized OpenTelemetry providers.
type Providers struct {
	TracerProvider *sdktrace.TracerProvider
}

// Setup initializes OpenTelemetry with stdout exporters.
// Returns a cleanup function that must be called on shutdown.
func Setup(ctx context.Context, serviceName, serviceVersion string, logger *slog.Logger) (*Providers, func(), error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.ServiceInstanceID(getEnvOrDefault("HOSTNAME", "unknown")),
			attribute.String("deployment.environment", getEnvOrDefault("ENV", "development")),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create otel resource: %w", err)
	}

	// Tracer provider
	tracerProvider, err := initTracerProvider(res, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init tracer provider: %w", err)
	}

	// Set global providers
	otel.SetTracerProvider(tracerProvider)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	providers := &Providers{
		TracerProvider: tracerProvider,
	}

	// Cleanup function flushes and shuts down all providers
	cleanup := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := tracerProvider.Shutdown(shutdownCtx); err != nil {
			logger.Error("tracer provider shutdown failed", "error", err)
		}
		logger.Info("otel providers shut down successfully")
	}

	logger.Info("otel initialized",
		"service", serviceName,
		"version", serviceVersion,
	)

	return providers, cleanup, nil
}

// Tracer returns the global tracer for this instrumentation.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// initTracerProvider creates a tracer provider with a stdout exporter.
func initTracerProvider(res *resource.Resource, logger *slog.Logger) (*sdktrace.TracerProvider, error) {
	exporter, err := stdouttrace.New(
		stdouttrace.WithWriter(os.Stdout),
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	return tp, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
