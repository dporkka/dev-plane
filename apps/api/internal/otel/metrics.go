package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metric names for the AI Dev Control Plane domain.
const (
	// AgentRunDurationMs records the duration of agent runs in milliseconds.
	AgentRunDurationMs = "agent_run_duration_ms"
	// AgentRunTotal counts the total number of agent runs by status.
	AgentRunTotal = "agent_run_total"
	// PRCreatedTotal counts the total number of pull requests created.
	PRCreatedTotal = "pr_created_total"
	// ModelCostTotal records the total cost of model API calls.
	ModelCostTotal = "model_cost_total"
	// ModelLatencyMs records the latency of model API calls in milliseconds.
	ModelLatencyMs = "model_latency_ms"
	// SandboxStartupMs records the time to start a sandbox environment.
	SandboxStartupMs = "sandbox_startup_ms"
	// CommandFailureTotal counts the total number of failed shell commands.
	CommandFailureTotal = "command_failure_total"
	// TestPassFail counts test results by pass/fail status.
	TestPassFail = "test_pass_fail"
	// ApprovalLatencyMs records the time from approval request to response.
	ApprovalLatencyMs = "approval_latency_ms"
	// StuckRunTotal counts the total number of stuck agent runs.
	StuckRunTotal = "stuck_run_total"
)

// MetricLabels provides common label/attribute keys.
const (
	LabelStatus   = "status"
	LabelModel    = "model"
	LabelProvider = "provider"
	LabelResult   = "result"
	LabelSeverity = "severity"
	LabelRule     = "rule"
	LabelAgent    = "agent_role"
	LabelTask     = "task_id"
	LabelProject  = "project_id"
)

// Float64HistogramRecorder is the interface for recording float64 histogram observations.
type Float64HistogramRecorder interface {
	Record(ctx context.Context, value float64, opts ...metric.RecordOption)
}

// Int64CounterAdder is the interface for adding to int64 counters.
type Int64CounterAdder interface {
	Add(ctx context.Context, value int64, opts ...metric.AddOption)
}

// Float64CounterAdder is the interface for adding to float64 counters.
type Float64CounterAdder interface {
	Add(ctx context.Context, value float64, opts ...metric.AddOption)
}

// CustomMetrics holds pre-initialized custom metric instruments.
type CustomMetrics struct {
	agentRunDuration    Float64HistogramRecorder
	agentRunTotal       Int64CounterAdder
	prCreatedTotal      Int64CounterAdder
	modelCostTotal      Float64CounterAdder
	modelLatencyMs      Float64HistogramRecorder
	sandboxStartupMs    Float64HistogramRecorder
	commandFailureTotal Int64CounterAdder
	testPassFail        Int64CounterAdder
	approvalLatencyMs   Float64HistogramRecorder
	stuckRunTotal       Int64CounterAdder
}

// NewCustomMetrics initializes all custom application metrics.
func NewCustomMetrics() (*CustomMetrics, error) {
	meter := otelMeter()

	agentRunDuration, err := meter.Float64Histogram(
		AgentRunDurationMs,
		metric.WithDescription("Agent run duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	agentRunTotal, err := meter.Int64Counter(
		AgentRunTotal,
		metric.WithDescription("Total number of agent runs"),
	)
	if err != nil {
		return nil, err
	}

	prCreatedTotal, err := meter.Int64Counter(
		PRCreatedTotal,
		metric.WithDescription("Total number of pull requests created"),
	)
	if err != nil {
		return nil, err
	}

	modelCostTotal, err := meter.Float64Counter(
		ModelCostTotal,
		metric.WithDescription("Total cost of model API calls in USD"),
		metric.WithUnit("USD"),
	)
	if err != nil {
		return nil, err
	}

	modelLatencyMs, err := meter.Float64Histogram(
		ModelLatencyMs,
		metric.WithDescription("Model API call latency in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	sandboxStartupMs, err := meter.Float64Histogram(
		SandboxStartupMs,
		metric.WithDescription("Sandbox startup time in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	commandFailureTotal, err := meter.Int64Counter(
		CommandFailureTotal,
		metric.WithDescription("Total number of failed shell commands"),
	)
	if err != nil {
		return nil, err
	}

	testPassFail, err := meter.Int64Counter(
		TestPassFail,
		metric.WithDescription("Test results by pass/fail status"),
	)
	if err != nil {
		return nil, err
	}

	approvalLatencyMs, err := meter.Float64Histogram(
		ApprovalLatencyMs,
		metric.WithDescription("Approval response latency in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	stuckRunTotal, err := meter.Int64Counter(
		StuckRunTotal,
		metric.WithDescription("Total number of stuck agent runs"),
	)
	if err != nil {
		return nil, err
	}

	return &CustomMetrics{
		agentRunDuration:    agentRunDuration,
		agentRunTotal:       agentRunTotal,
		prCreatedTotal:      prCreatedTotal,
		modelCostTotal:      modelCostTotal,
		modelLatencyMs:      modelLatencyMs,
		sandboxStartupMs:    sandboxStartupMs,
		commandFailureTotal: commandFailureTotal,
		testPassFail:        testPassFail,
		approvalLatencyMs:   approvalLatencyMs,
		stuckRunTotal:       stuckRunTotal,
	}, nil
}

// RecordAgentRunDuration records the duration of an agent run.
func (m *CustomMetrics) RecordAgentRunDuration(ctx context.Context, durationMs float64, status string) {
	m.agentRunDuration.Record(ctx, durationMs, metric.WithAttributes(attribute.String(LabelStatus, status)))
}

// RecordAgentRun records an agent run occurrence.
func (m *CustomMetrics) RecordAgentRun(ctx context.Context, status string) {
	m.agentRunTotal.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelStatus, status)))
}

// RecordPRCreated records a pull request creation.
func (m *CustomMetrics) RecordPRCreated(ctx context.Context) {
	m.prCreatedTotal.Add(ctx, 1)
}

// RecordModelCost records model API call cost.
func (m *CustomMetrics) RecordModelCost(ctx context.Context, cost float64, model, provider string) {
	m.modelCostTotal.Add(ctx, cost,
		metric.WithAttributes(
			attribute.String(LabelModel, model),
			attribute.String(LabelProvider, provider),
		),
	)
}

// RecordModelLatency records model API call latency.
func (m *CustomMetrics) RecordModelLatency(ctx context.Context, latencyMs float64, model string) {
	m.modelLatencyMs.Record(ctx, latencyMs, metric.WithAttributes(attribute.String(LabelModel, model)))
}

// RecordSandboxStartup records sandbox startup time.
func (m *CustomMetrics) RecordSandboxStartup(ctx context.Context, durationMs float64) {
	m.sandboxStartupMs.Record(ctx, durationMs)
}

// RecordCommandFailure records a failed shell command.
func (m *CustomMetrics) RecordCommandFailure(ctx context.Context) {
	m.commandFailureTotal.Add(ctx, 1)
}

// RecordTestResult records a test result.
func (m *CustomMetrics) RecordTestResult(ctx context.Context, passed bool) {
	result := "fail"
	if passed {
		result = "pass"
	}
	m.testPassFail.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelResult, result)))
}

// RecordApprovalLatency records approval response latency.
func (m *CustomMetrics) RecordApprovalLatency(ctx context.Context, latencyMs float64) {
	m.approvalLatencyMs.Record(ctx, latencyMs)
}

// RecordStuckRun records a stuck agent run.
func (m *CustomMetrics) RecordStuckRun(ctx context.Context) {
	m.stuckRunTotal.Add(ctx, 1)
}
