package runtimes

import (
	"context"
	"net/http"
	"net/url"
)

// GetUsage returns reconciled Nulang Cloud runtime usage for a workspace.
// Model/token cost remains outside this contract and belongs to the model gateway.
func (p *NulangCloudProvider) GetUsage(ctx context.Context, sessionID string) (*RuntimeUsage, error) {
	var usage RuntimeUsage
	if err := p.doJSON(ctx, http.MethodGet, "/workspaces/"+url.PathEscape(sessionID)+"/usage", nil, &usage, ""); err != nil {
		return nil, err
	}
	return &usage, nil
}

var _ UsageProvider = (*NulangCloudProvider)(nil)
