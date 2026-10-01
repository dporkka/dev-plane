package runtimes

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	now := time.Unix(100, 0)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		RecoveryTimeout:  30 * time.Second,
	}).withClock(func() time.Time { return now })

	if err := breaker.Before(); err != nil {
		t.Fatalf("Before() initial error = %v", err)
	}
	breaker.RecordFailure()
	if err := breaker.Before(); err != nil {
		t.Fatalf("Before() after one failure error = %v", err)
	}
	breaker.RecordFailure()

	if err := breaker.Before(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Before() error = %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreakerAllowsSingleHalfOpenProbeAndRecovers(t *testing.T) {
	now := time.Unix(100, 0)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		RecoveryTimeout:  10 * time.Second,
	}).withClock(func() time.Time { return now })

	breaker.RecordFailure()
	if err := breaker.Before(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Before() error = %v, want ErrCircuitOpen", err)
	}

	now = now.Add(11 * time.Second)
	if err := breaker.Before(); err != nil {
		t.Fatalf("half-open probe Before() error = %v", err)
	}
	if err := breaker.Before(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("second half-open probe error = %v, want ErrCircuitOpen", err)
	}

	breaker.RecordSuccess()
	if err := breaker.Before(); err != nil {
		t.Fatalf("Before() after recovery error = %v", err)
	}
}

func TestCircuitBreakerFailedProbeReopens(t *testing.T) {
	now := time.Unix(100, 0)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		RecoveryTimeout:  10 * time.Second,
	}).withClock(func() time.Time { return now })

	breaker.RecordFailure()
	now = now.Add(11 * time.Second)
	if err := breaker.Before(); err != nil {
		t.Fatalf("probe Before() error = %v", err)
	}
	breaker.RecordFailure()

	if err := breaker.Before(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Before() after failed probe = %v, want ErrCircuitOpen", err)
	}
}
