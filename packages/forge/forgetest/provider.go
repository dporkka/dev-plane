// Package forgetest provides deterministic forge test doubles.
package forgetest

import (
	"context"
	"fmt"
	"sync"

	"github.com/ai-dev-control-plane/forge"
)

// Provider is an in-memory forge implementation suitable for unit and contract tests.
type Provider struct {
	mu      sync.Mutex
	next    int
	changes map[int]*forge.Change
}

// NewProvider returns a fresh deterministic in-memory provider.
func NewProvider() *Provider {
	return &Provider{
		next:    1,
		changes: make(map[int]*forge.Change),
	}
}

// Name implements forge.Provider.
func (p *Provider) Name() string { return "memory" }

// OpenChange implements forge.Provider.
func (p *Provider) OpenChange(_ context.Context, _ forge.Credential, repository forge.Repository, req forge.OpenChangeRequest) (*forge.Change, error) {
	if err := forge.ValidateOpenChangeRequest(repository, req); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	number := p.next
	p.next++

	change := &forge.Change{
		Number:       number,
		Title:        req.Title,
		Body:         req.Body,
		URL:          fmt.Sprintf("https://forge.invalid/%s/%s/changes/%d", repository.Namespace, repository.Name, number),
		State:        forge.ChangeStateOpen,
		Head:         req.Head,
		Base:         req.Base,
		HeadRevision: fmt.Sprintf("head-%d", number),
		Draft:        req.Draft,
	}
	p.changes[number] = change

	copy := *change
	return &copy, nil
}

// MergeChange implements forge.Provider.
func (p *Provider) MergeChange(_ context.Context, _ forge.Credential, repository forge.Repository, number int, req forge.MergeChangeRequest) (*forge.MergeResult, error) {
	if err := forge.ValidateMergeChangeRequest(repository, number, req); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	change, ok := p.changes[number]
	if !ok {
		return nil, fmt.Errorf("%w: change %d", forge.ErrNotFound, number)
	}
	if change.State != forge.ChangeStateOpen {
		return nil, fmt.Errorf("%w: change %d is %s", forge.ErrConflict, number, change.State)
	}

	change.State = forge.ChangeStateMerged
	return &forge.MergeResult{
		Merged:   true,
		Revision: fmt.Sprintf("merge-%d", number),
		Message:  string(forge.NormalizeMergeMethod(req.Method)),
	}, nil
}

var _ forge.Provider = (*Provider)(nil)
