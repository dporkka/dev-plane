package egresspolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SessionIdentity must be resolved from a trusted authorization source that
// verifies the calling principal is permitted to operate on the runtime
// session. Never derive tenant or agent IDs from sandbox files or HTTP headers.
type SessionIdentity struct {
	RuntimeSessionID string
	TenantID         string
	AgentID          string
}

// SessionResolver is implemented by the trusted scheduler or database layer.
// Its contract includes authorization of the caller carried in ctx, not just
// looking up a record by session ID.
type SessionResolver interface {
	ResolveAuthorizedSession(ctx context.Context, runtimeSessionID string) (SessionIdentity, error)
}

// WorkspaceWriter is the trusted host-side runtime file API. It must constrain
// destinations to the specific container workspace and validate file paths.
type WorkspaceWriter interface {
	WriteFile(ctx context.Context, runtimeSessionID, path string, data []byte) error
}

// WorkspaceFetchReceipt is a bounded output envelope; it intentionally omits
// destination URLs and all request/response headers except Content-Type.
// The JSON encoding of Body is base64 (Go's standard []byte representation).
type WorkspaceFetchReceipt struct {
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body"`
}

// WorkspaceBridge mediates a narrow outbound GET into an isolated workspace.
// No endpoint or automatic agent tool is registered by this package.
type WorkspaceBridge struct {
	broker   *Broker
	resolver SessionResolver
	writer   WorkspaceWriter
}

func NewWorkspaceBridge(broker *Broker, resolver SessionResolver, writer WorkspaceWriter) *WorkspaceBridge {
	return &WorkspaceBridge{broker: broker, resolver: resolver, writer: writer}
}

func validReceiptID(value string) bool {
	if len(value) < 1 || len(value) > 80 {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func (b *WorkspaceBridge) FetchToWorkspace(ctx context.Context, runtimeSessionID, receiptID, destination string) error {
	if ctx == nil || ctx.Err() != nil || b == nil || b.broker == nil || b.resolver == nil || b.writer == nil ||
		!validReceiptID(receiptID) || strings.TrimSpace(runtimeSessionID) != runtimeSessionID || runtimeSessionID == "" {
		return ErrDenied
	}
	identity, err := b.resolver.ResolveAuthorizedSession(ctx, runtimeSessionID)
	if err != nil || identity.RuntimeSessionID != runtimeSessionID || identity.TenantID == "" || identity.AgentID == "" ||
		strings.TrimSpace(identity.TenantID) != identity.TenantID || strings.TrimSpace(identity.AgentID) != identity.AgentID {
		return ErrDenied
	}

	response, err := b.broker.Fetch(ctx, identity.TenantID, identity.AgentID, destination)
	if err != nil {
		return err
	}
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%w: upstream request did not succeed", ErrDenied)
	}

	data, err := json.Marshal(WorkspaceFetchReceipt{
		StatusCode:  response.StatusCode,
		ContentType: response.ContentType,
		Body:        response.Body,
	})
	if err != nil {
		return errors.New("egress response encoding failed")
	}
	return b.writer.WriteFile(ctx, runtimeSessionID, ".dev-plane/egress/"+receiptID+".json", data)
}
