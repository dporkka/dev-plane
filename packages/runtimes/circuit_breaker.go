package runtimes

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("runtime provider circuit is open")

type CircuitBreakerConfig struct {
	FailureThreshold int
	RecoveryTimeout  time.Duration
}

type circuitState uint8

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

type CircuitBreaker struct {
	mu               sync.Mutex
	cfg              CircuitBreakerConfig
	state            circuitState
	failures         int
	openedAt         time.Time
	halfOpenInFlight bool
	now              func() time.Time
}

func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 30 * time.Second
	}
	return &CircuitBreaker{
		cfg: cfg,
		now: time.Now,
	}
}

func (b *CircuitBreaker) withClock(now func() time.Time) *CircuitBreaker {
	if now != nil {
		b.now = now
	}
	return b
}

func (b *CircuitBreaker) Before() error {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case circuitClosed:
		return nil
	case circuitOpen:
		if b.now().Sub(b.openedAt) < b.cfg.RecoveryTimeout {
			return ErrCircuitOpen
		}
		b.state = circuitHalfOpen
		b.halfOpenInFlight = true
		return nil
	case circuitHalfOpen:
		if b.halfOpenInFlight {
			return ErrCircuitOpen
		}
		b.halfOpenInFlight = true
		return nil
	default:
		return ErrCircuitOpen
	}
}

func (b *CircuitBreaker) RecordSuccess() {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = circuitClosed
	b.failures = 0
	b.openedAt = time.Time{}
	b.halfOpenInFlight = false
}

func (b *CircuitBreaker) RecordFailure() {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == circuitHalfOpen {
		b.state = circuitOpen
		b.openedAt = b.now()
		b.halfOpenInFlight = false
		b.failures = b.cfg.FailureThreshold
		return
	}

	b.failures++
	if b.failures >= b.cfg.FailureThreshold {
		b.state = circuitOpen
		b.openedAt = b.now()
		b.halfOpenInFlight = false
	}
}
