package otel

import (
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// Middleware returns HTTP middleware that traces incoming requests with OpenTelemetry.
// It extracts trace context from incoming requests, creates a span for the request,
// records HTTP metrics (duration, status code), and adds the trace ID to response headers.
func Middleware(next http.Handler) http.Handler {
	tracer := Tracer()
	meter := otelMeter()

	// Initialize HTTP metrics
	requestDuration, err := meter.Float64Histogram(
		"http_request_duration_ms",
		metric.WithDescription("HTTP request duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	recordDuration := err == nil

	requestCount, err := meter.Int64Counter(
		"http_request_total",
		metric.WithDescription("Total number of HTTP requests"),
	)
	recordCount := err == nil

	propagator := propagation.TraceContext{}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Start span with HTTP semantic conventions
		ctx, span := tracer.Start(ctx, fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", r.URL.Path),
				attribute.String("http.target", r.URL.RequestURI()),
				attribute.String("http.scheme", r.URL.Scheme),
				attribute.String("http.host", r.Host),
				attribute.String("http.client_ip", r.RemoteAddr),
				attribute.String("http.user_agent", r.UserAgent()),
				attribute.String("http.request_content_length", fmt.Sprintf("%d", r.ContentLength)),
			),
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Add trace ID to response header for client-side correlation
		traceID := span.SpanContext().TraceID().String()
		w.Header().Set("X-Trace-ID", traceID)

		// Wrap response writer to capture status code
		wr := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Call the next handler
		next.ServeHTTP(wr, r.WithContext(ctx))

		// Record span attributes and status
		duration := time.Since(start)
		span.SetAttributes(
			attribute.Int("http.status_code", wr.statusCode),
			attribute.Int64("http.duration_ms", duration.Milliseconds()),
		)

		if wr.statusCode >= 400 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", wr.statusCode))
		}

		// Record metrics
		if recordDuration {
			requestDuration.Record(ctx, float64(duration.Milliseconds()))
		}
		if recordCount {
			requestCount.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.route", r.URL.Path),
					attribute.Int("http.status_code", wr.statusCode),
				),
			)
		}
	})
}

// otelMeter returns the global meter.
func otelMeter() metric.Meter {
	return otel.GetMeterProvider().Meter(instrumentationName)
}
