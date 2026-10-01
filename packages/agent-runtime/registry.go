package agentruntime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrProviderNotFound is returned when no adapter is registered by the requested name.
	ErrProviderNotFound = errors.New("agent runtime provider not found")
	// ErrProviderAlreadyRegistered is returned when a normalized provider name already exists.
	ErrProviderAlreadyRegistered = errors.New("agent runtime provider already registered")
	// ErrCapabilityContract is returned when a provider advertises behavior it cannot execute.
	ErrCapabilityContract = errors.New("agent runtime capability contract mismatch")
)

// Registry stores provider adapters behind normalized names.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider after verifying that its advertised capabilities
// match the optional interfaces implemented by the adapter.
func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("%w: nil provider", ErrCapabilityContract)
	}

	name := normalizeProviderName(provider.Name())
	if name == "" {
		return fmt.Errorf("%w: provider name is required", ErrCapabilityContract)
	}
	if err := validateCapabilityContract(provider); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: %s", ErrProviderAlreadyRegistered, name)
	}
	r.providers[name] = provider
	return nil
}

// Get returns a registered provider using case-insensitive, whitespace-trimmed lookup.
func (r *Registry) Get(name string) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, normalizeProviderName(name))
	}

	normalized := normalizeProviderName(name)
	r.mu.RLock()
	provider, ok := r.providers[normalized]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, normalized)
	}
	return provider, nil
}

// Names returns normalized registered provider names in stable order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	r.mu.RUnlock()

	sort.Strings(names)
	return names
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func validateCapabilityContract(provider Provider) error {
	capabilities := provider.Capabilities()
	checks := []struct {
		capability Capability
		supported  bool
	}{
		{CapabilityResumeThread, implementsResume(provider)},
		{CapabilityInterruptTurn, implementsInterrupt(provider)},
		{CapabilitySteerTurn, implementsSteering(provider)},
		{CapabilityRollback, implementsRollback(provider)},
		{CapabilityCompact, implementsCompaction(provider)},
		{CapabilityApprovalRequests, implementsApproval(provider)},
		{CapabilityQuestions, implementsQuestion(provider)},
	}

	for _, check := range checks {
		if capabilities.Supports(check.capability) && !check.supported {
			return fmt.Errorf(
				"%w: provider %q advertises %s without implementing its interface",
				ErrCapabilityContract,
				normalizeProviderName(provider.Name()),
				check.capability.String(),
			)
		}
	}
	return nil
}

func implementsResume(provider Provider) bool {
	_, ok := provider.(ResumeProvider)
	return ok
}

func implementsInterrupt(provider Provider) bool {
	_, ok := provider.(InterruptProvider)
	return ok
}

func implementsSteering(provider Provider) bool {
	_, ok := provider.(SteeringProvider)
	return ok
}

func implementsRollback(provider Provider) bool {
	_, ok := provider.(RollbackProvider)
	return ok
}

func implementsCompaction(provider Provider) bool {
	_, ok := provider.(CompactionProvider)
	return ok
}

func implementsApproval(provider Provider) bool {
	_, ok := provider.(ApprovalProvider)
	return ok
}

func implementsQuestion(provider Provider) bool {
	_, ok := provider.(QuestionProvider)
	return ok
}
