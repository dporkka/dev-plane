package verifier

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

// RemoteCIQuery selects one immutable CI attempt. All IDs are supplied by the
// operator's authorized workflow, not taken from a webhook's claimed success.
type RemoteCIQuery struct {
	HeadSHA    string
	PipelineID string
	RunID      string
	JobID      string
	AttemptID  string
}

// AuthoritativeCIReader is implemented by an authenticated provider API client.
// The adapter MUST fetch execution facts from the authoritative CI service,
// not merely copy incoming event data or GitHub commit-status text.
type AuthoritativeCIReader interface {
	FetchAttempt(ctx context.Context, query RemoteCIQuery) (repoprotocol.RemoteCIRecord, error)
}

// RemoteCIAttestor translates independently fetched provider facts into a
// signed observation. Signer keys must never be available to job containers,
// coding agents, or untrusted CI pipeline steps.
type RemoteCIAttestor struct {
	Source     AuthoritativeCIReader
	Policy     repoprotocol.RemoteCIPolicy
	PrivateKey ed25519.PrivateKey
}

func (a RemoteCIAttestor) Attest(ctx context.Context, query RemoteCIQuery) (repoprotocol.RemoteCIReceipt, error) {
	if a.Source == nil {
		return repoprotocol.RemoteCIReceipt{}, errors.New("authoritative CI source is required")
	}
	if len(a.PrivateKey) != ed25519.PrivateKeySize ||
		len(a.Policy.PublicKey) != ed25519.PublicKeySize ||
		!a.PrivateKey.Public().(ed25519.PublicKey).Equal(a.Policy.PublicKey) {
		return repoprotocol.RemoteCIReceipt{}, errors.New("attestor signing key does not match trusted policy")
	}
	if query.HeadSHA == "" || query.PipelineID == "" || query.RunID == "" ||
		query.JobID == "" || query.AttemptID == "" {
		return repoprotocol.RemoteCIReceipt{}, errors.New("exact CI attempt query is required")
	}
	// Fail before querying or signing if the request differs from the trusted
	// server-managed release target. A caller cannot authorize its own retry.
	if a.Policy.PipelineID == "" || a.Policy.RunID == "" ||
		a.Policy.JobID == "" || a.Policy.AttemptID == "" ||
		query.PipelineID != a.Policy.PipelineID || query.RunID != a.Policy.RunID ||
		query.JobID != a.Policy.JobID || query.AttemptID != a.Policy.AttemptID {
		return repoprotocol.RemoteCIReceipt{}, errors.New("CI signing request does not match trusted exact attempt")
	}
	record, err := a.Source.FetchAttempt(ctx, query)
	if err != nil {
		return repoprotocol.RemoteCIReceipt{}, fmt.Errorf("fetch authoritative CI attempt: %w", err)
	}
	if record.HeadSHA != query.HeadSHA || record.PipelineID != query.PipelineID ||
		record.RunID != query.RunID || record.JobID != query.JobID ||
		record.AttemptID != query.AttemptID {
		return repoprotocol.RemoteCIReceipt{}, errors.New("CI provider response does not match requested exact attempt")
	}
	if record.Provider != a.Policy.Provider || record.Repository != a.Policy.Repository ||
		record.DefinitionSHA256 != a.Policy.DefinitionSHA256 {
		return repoprotocol.RemoteCIReceipt{}, errors.New("CI provider response does not match pinned pipeline policy")
	}
	receipt, err := repoprotocol.SignRemoteCIReceipt(record, a.Policy.SignerKeyID, a.PrivateKey)
	if err != nil {
		return repoprotocol.RemoteCIReceipt{}, fmt.Errorf("sign provider CI evidence: %w", err)
	}
	return receipt, nil
}
