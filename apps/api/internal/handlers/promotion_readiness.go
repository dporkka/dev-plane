package handlers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	promotion "github.com/ai-dev-control-plane/promotion"
	githubpromotion "github.com/ai-dev-control-plane/promotion/github"
	protocol "github.com/ai-dev-control-plane/repoprotocol"
)

type promotionObserver interface {
	Observe(ctx context.Context, repository string, prNumber int) (githubpromotion.Observation, error)
}

// WithPromotionObserver injects a read-only provider adapter, primarily for contract tests.
func (h *Handler) WithPromotionObserver(observer promotionObserver) *Handler {
	h.promotionObserver = observer
	return h
}

type promotionReadinessResponse struct {
	Decision           promotion.Decision `json:"decision"`
	Repository         string             `json:"repository,omitempty"`
	PRNumber           int                `json:"pr_number,omitempty"`
	SourceHeadSHA      string             `json:"source_head_sha,omitempty"`
	TargetHeadSHA      string             `json:"target_head_sha,omitempty"`
	CandidateCommitSHA string             `json:"candidate_commit_sha,omitempty"`
	PolicyDigest       string             `json:"policy_digest,omitempty"`
	PolicyRevisionSHA  string             `json:"policy_revision_sha,omitempty"`
}

// GetPullRequestPromotionReadiness returns a *dry-run* decision for a stored attempt.
// It neither creates evidence nor modifies Git, PR reviews, branch refs, or local task state.
func (h *Handler) GetPullRequestPromotionReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	attemptID := strings.TrimSpace(r.URL.Query().Get("attempt_id"))
	if id == "" || attemptID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id and attempt_id are required"))
		return
	}
	if err := authz.AuthorizePullRequest(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}

	var number int
	var owner, repoName, taskID, prState string
	err := h.db.QueryRowContext(ctx, `SELECT pr.number, r.owner, r.name, pr.task_id, pr.state
        FROM pull_requests pr
        JOIN repositories r ON r.id = pr.repository_id
        WHERE pr.id = $1`, id).
		Scan(&number, &owner, &repoName, &taskID, &prState)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		} else {
			respond.Error(w, http.StatusInternalServerError, errors.New("unable to load pull request"))
		}
		return
	}
	repository := owner + "/" + repoName
	out := promotionReadinessResponse{Repository: repository, PRNumber: number}
	block := func(code promotion.BlockerCode) {
		out.Decision = promotion.Decision{
			Eligible: false, DryRun: true, AttemptID: attemptID,
			Blockers: []promotion.BlockerCode{code},
		}
		respond.JSON(w, http.StatusOK, out)
	}
	if prState != "open" {
		block(promotion.BlockInvalidSnapshot)
		return
	}

	// Never accept structurally valid but unauthenticated DB rows as promotion proof.
	// The verifier's HMAC secret must be provisioned only for trusted verifier
	// workers and this read-only assessor, never for coding agents or CI jobs.
	signingKey, keyErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("PROMOTION_ATTESTATION_KEY_BASE64")))
	if keyErr != nil || len(signingKey) < 32 {
		block(promotion.BlockInvalidEvidence)
		return
	}

	// An attempt must be associated with *this* PR task and repository.
	// Cross-tenant/cross-task evidence must not be accepted merely by attempt ID.
	var raw []byte
	err = h.db.QueryRowContext(ctx, `SELECT payload FROM verification_attempts
        WHERE id = $1 AND work_item_id = $2 AND repository = $3`,
		attemptID, taskID, repository).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		block(promotion.BlockMissingAttempt)
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("unable to load verification attempt"))
		return
	}
	var attempt protocol.VerificationAttempt
	if err := json.Unmarshal(raw, &attempt); err != nil || attempt.ID != attemptID ||
		attempt.WorkItemID != taskID || attempt.Repository != repository ||
		attempt.Validate() != nil ||
		protocol.VerifyVerificationAttemptSignature(attempt, signingKey) != nil {
		block(promotion.BlockInvalidEvidence)
		return
	}

	reader := h.promotionObserver
	if reader == nil {
		token := strings.TrimSpace(h.githubToken)
		if token == "" {
			token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
		}
		if token == "" {
			block(promotion.BlockProviderUnavailable)
			return
		}
		reader = githubpromotion.NewReader(token)
	}
	observed, err := reader.Observe(ctx, repository, number)
	if err != nil {
		h.logger.Warn("promotion observation unavailable", "pr_id", id, "error", err)
		block(promotion.BlockProviderUnavailable)
		return
	}
	if observed.Snapshot.Repository != repository || observed.PolicyRevisionSHA != observed.Snapshot.CurrentTargetHeadSHA {
		block(promotion.BlockInvalidSnapshot)
		return
	}
	out.SourceHeadSHA = observed.Snapshot.CurrentSourceHeadSHA
	out.TargetHeadSHA = observed.Snapshot.CurrentTargetHeadSHA
	out.CandidateCommitSHA = observed.Snapshot.CandidateCommitSHA
	out.PolicyDigest = observed.Policy.Digest
	out.PolicyRevisionSHA = observed.PolicyRevisionSHA
	out.Decision = promotion.Evaluate(promotion.Request{
		Snapshot: observed.Snapshot, Policy: observed.Policy, Attempt: attempt,
		AgentActorID: observed.AgentActorID, Approvals: observed.Approvals,
	})
	respond.JSON(w, http.StatusOK, out)
}
