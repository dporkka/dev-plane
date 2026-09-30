// Package handlers provides HTTP handlers for the API service.
//
// Pull request handlers manage PRs created by the agent system:
//   - GET /projects/{projectID}/pull-requests  -> list PRs for a project
//   - GET /pull-requests/{id}                   -> get PR by ID
//   - POST /tasks/{taskId}/pull-request         -> create PR for a task
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/prfactory"
)

// PullRequestResponse is the API representation of a pull request.
type PullRequestResponse struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	RunID      *string    `json:"run_id,omitempty"`
	RepoID     string     `json:"repository_id"`
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Branch     string     `json:"branch"`
	BaseBranch string     `json:"base_branch"`
	URL        string     `json:"url"`
	State      string     `json:"state"`
	Draft      bool       `json:"draft"`
	CreatedBy  string     `json:"created_by"`
	MergedAt   *time.Time `json:"merged_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ListPullRequests returns PRs for a project.
func (h *Handler) ListPullRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	if err := authz.AuthorizeProject(ctx, h.db, user, projectID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("project not found"))
		return
	}

	// Get repository IDs for the project
	rows, err := h.db.QueryContext(ctx, `
		SELECT id FROM repositories WHERE project_id = $1
	`, projectID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var repoIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		repoIDs = append(repoIDs, id)
	}

	if len(repoIDs) == 0 {
		respond.JSON(w, http.StatusOK, []PullRequestResponse{})
		return
	}

	// Build parameterized query for IN clause
	query := `
		SELECT id, task_id, run_id, repository_id, number, title, body,
		       branch, base_branch, url, state, draft, created_by, merged_at, created_at, updated_at
		FROM pull_requests
		WHERE repository_id IN (`
	args := make([]interface{}, len(repoIDs))
	for i, id := range repoIDs {
		if i > 0 {
			query += ", "
		}
		query += fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	query += `) ORDER BY created_at DESC`

	prRows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer prRows.Close()

	var prs []PullRequestResponse
	for prRows.Next() {
		var pr PullRequestResponse
		var runID sql.NullString
		var mergedAt sql.NullTime
		err := prRows.Scan(
			&pr.ID, &pr.TaskID, &runID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Body,
			&pr.Branch, &pr.BaseBranch, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedBy,
			&mergedAt, &pr.CreatedAt, &pr.UpdatedAt,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if runID.Valid {
			pr.RunID = &runID.String
		}
		if mergedAt.Valid {
			pr.MergedAt = &mergedAt.Time
		}
		prs = append(prs, pr)
	}

	if prs == nil {
		prs = []PullRequestResponse{}
	}
	respond.JSON(w, http.StatusOK, prs)
}

// GetPullRequest returns a PR by ID.
func (h *Handler) GetPullRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id is required"))
		return
	}

	if err := authz.AuthorizePullRequest(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}

	var pr PullRequestResponse
	var runID sql.NullString
	var mergedAt sql.NullTime

	err := h.db.QueryRowContext(ctx, `
		SELECT id, task_id, run_id, repository_id, number, title, body,
		       branch, base_branch, url, state, draft, created_by, merged_at, created_at, updated_at
		FROM pull_requests WHERE id = $1
	`, id).Scan(
		&pr.ID, &pr.TaskID, &runID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Body,
		&pr.Branch, &pr.BaseBranch, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedBy,
		&mergedAt, &pr.CreatedAt, &pr.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if runID.Valid {
		pr.RunID = &runID.String
	}
	if mergedAt.Valid {
		pr.MergedAt = &mergedAt.Time
	}

	respond.JSON(w, http.StatusOK, pr)
}

// DecisionPacketResponse is the immutable approval snapshot bound to a verified candidate.
type DecisionPacketResponse struct {
	ID          string                `json:"id"`
	CandidateID string                `json:"candidate_id"`
	Digest      string                `json:"digest"`
	Packet      decisionpacket.Packet `json:"packet"`
	CreatedAt   time.Time             `json:"created_at"`
}

// GetPullRequestDecisionPacket returns the immutable decision packet for a verified PR.
func (h *Handler) GetPullRequestDecisionPacket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id is required"))
		return
	}
	if err := authz.AuthorizePullRequest(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}

	var response DecisionPacketResponse
	var raw string
	err := h.db.QueryRowContext(ctx, `
		SELECT dp.id, dp.candidate_id, dp.digest, dp.packet, dp.created_at
		FROM decision_packets dp
		JOIN change_candidates c ON c.id = dp.candidate_id
		WHERE c.pull_request_id = $1
		LIMIT 1
	`, id).Scan(&response.ID, &response.CandidateID, &response.Digest, &raw, &response.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("decision packet not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if err := json.Unmarshal([]byte(raw), &response.Packet); err != nil {
		respond.Error(w, http.StatusConflict, fmt.Errorf("decode decision packet: %w", err))
		return
	}
	if err := response.Packet.Validate(); err != nil {
		respond.Error(w, http.StatusConflict, fmt.Errorf("invalid decision packet: %w", err))
		return
	}
	digest, err := response.Packet.Digest()
	if err != nil {
		respond.Error(w, http.StatusConflict, fmt.Errorf("digest decision packet: %w", err))
		return
	}
	if digest != response.Digest || response.Packet.Candidate.ID != response.CandidateID || response.Packet.Candidate.PullRequestID != id {
		respond.Error(w, http.StatusConflict, errors.New("decision packet integrity check failed"))
		return
	}

	respond.JSON(w, http.StatusOK, response)
}

// CreatePullRequestRequest is the request body for creating a PR.
type CreatePullRequestRequest struct {
	Approved bool `json:"approved,omitempty"`
}

// CreatePullRequest creates a PR for a task after human approval.
func (h *Handler) CreatePullRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskId")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	if err := authz.AuthorizeTask(ctx, h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	// Parse optional request body
	var req CreatePullRequestRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Verify task exists and is in a valid state for PR creation
	var task struct {
		Status   string
		RepoID   string
		Branch   string
	}
	err := h.db.QueryRowContext(ctx, `
		SELECT status, repository_id, target_branch
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&task.Status, &task.RepoID, &task.Branch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Allow PR creation from reviewing or pr_created states
	if task.Status != "reviewing" && task.Status != "pr_created" {
		respond.Error(w, http.StatusBadRequest,
			fmt.Errorf("task must be in 'reviewing' or 'pr_created' status, current: %s", task.Status))
		return
	}

	// Check for pending approvals if not explicitly approved via request
	if !req.Approved {
		var pendingCount int
		err := h.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM approvals
			WHERE task_id = $1 AND response IS NULL
			AND (expires_at IS NULL OR expires_at > $2)
		`, taskID, time.Now().UTC()).Scan(&pendingCount)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if pendingCount > 0 {
			respond.Error(w, http.StatusConflict, errors.New("pending approval exists for this task"))
			return
		}
	}

	// Create the pull request using the factory
	factory := prfactory.NewFactory(h.db, h.logger)
	pr, err := factory.CreatePullRequest(ctx, taskID)
	if err != nil {
		h.logger.Error("failed to create pull request", "task_id", taskID, "error", err)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("create pull request: %w", err))
		return
	}

	// Publish pr.created event
	if h.eventBus != nil {
		event := map[string]interface{}{
			"pr_id":      pr.ID,
			"task_id":    taskID,
			"pr_number":  pr.Number,
			"branch":     pr.Branch,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish("pr.created", data); pubErr != nil {
			h.logger.Warn("failed to publish pr.created event", "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusCreated, PullRequestResponse{
		ID:         pr.ID,
		TaskID:     pr.TaskID,
		RunID:      pr.RunID,
		RepoID:     pr.RepoID,
		Number:     pr.Number,
		Title:      pr.Title,
		Body:       pr.Body,
		Branch:     pr.Branch,
		BaseBranch: pr.BaseBranch,
		URL:        pr.URL,
		State:      pr.State,
		Draft:      pr.Draft,
		CreatedBy:  pr.CreatedBy,
		MergedAt:   pr.MergedAt,
		CreatedAt:  pr.CreatedAt,
		UpdatedAt:  pr.UpdatedAt,
	})
}

// MergePullRequestRequest is the request body for merging a pull request.
type MergePullRequestRequest struct {
	// Method is the merge method: "merge", "squash", or "rebase". Defaults to "merge".
	Method string `json:"merge_method,omitempty"`
	// SHA is the expected HEAD SHA of the pull request. When provided, the merge
	// fails if the pull request HEAD does not match.
	SHA string `json:"sha,omitempty"`
}

// MergePullRequest merges a pull request on GitHub after capability authorization.
// It updates the local PR record to merged and transitions the task to done.
func (h *Handler) MergePullRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id is required"))
		return
	}

	if err := authz.AuthorizePullRequest(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}

	var req MergePullRequestRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	var pr PullRequestResponse
	var runID sql.NullString
	var mergedAt sql.NullTime
	var repoOwner, repoName, taskID, taskStatus string

	err := h.db.QueryRowContext(ctx, `
		SELECT pr.id, pr.task_id, pr.run_id, pr.repository_id, pr.number, pr.title, pr.body,
		       pr.branch, pr.base_branch, pr.url, pr.state, pr.draft, pr.created_by, pr.merged_at,
		       pr.created_at, pr.updated_at, r.owner, r.name, t.status
		FROM pull_requests pr
		JOIN repositories r ON r.id = pr.repository_id
		JOIN tasks t ON t.id = pr.task_id
		WHERE pr.id = $1
	`, id).Scan(
		&pr.ID, &taskID, &runID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Body,
		&pr.Branch, &pr.BaseBranch, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedBy,
		&mergedAt, &pr.CreatedAt, &pr.UpdatedAt, &repoOwner, &repoName, &taskStatus,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	pr.TaskID = taskID
	if runID.Valid {
		pr.RunID = &runID.String
	}
	if mergedAt.Valid {
		pr.MergedAt = &mergedAt.Time
	}

	if pr.State == "merged" {
		respond.Error(w, http.StatusConflict, errors.New("pull request is already merged"))
		return
	}
	if taskStatus != "pr_created" {
		respond.Error(w, http.StatusBadRequest,
			fmt.Errorf("task must be in 'pr_created' status, current: %s", taskStatus))
		return
	}

	// Capability kernel authorization. Merge is admin-only by default.
	result, err := h.kernel().Evaluate(ctx, capability.Request{
		ActorType: "human",
		User: &models.User{
			ID:             user.UserID,
			OrganizationID: user.OrgID,
			Role:           user.Role,
		},
		Operation: capability.OpMergePR,
		Resource:  fmt.Sprintf("%s/%s#%d", repoOwner, repoName, pr.Number),
		Details: map[string]any{
			"organization_id": user.OrgID,
			"pull_request_id": id,
		},
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("authorize merge: %w", err))
		return
	}
	if result.Effect == policies.EffectDeny {
		respond.Error(w, http.StatusForbidden, errors.New(result.Reason))
		return
	}
	if result.RequiredApproval {
		respond.Error(w, http.StatusLocked, errors.New(result.Reason))
		return
	}

	token := strings.TrimSpace(h.githubToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("github token is not configured"))
		return
	}

	gh := h.githubGateway
	if gh == nil {
		gh = gateway.NewGitHubGateway(os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"))
	}

	verified, err := h.loadVerifiedCandidateForMerge(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusConflict, errors.New("pull request has no verified candidate evidence"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("load verified candidate: %w", err))
		return
	}
	if err := verified.Validate(); err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	if req.SHA != "" && req.SHA != verified.CommitSHA {
		respond.Error(w, http.StatusConflict, errors.New("requested sha does not match verified candidate"))
		return
	}

	mergeResult, err := gh.MergePR(ctx, &oauth2.Token{AccessToken: token}, repoOwner, repoName, pr.Number, gateway.MergePRRequest{
		Method: req.Method,
		SHA:    verified.CommitSHA,
	})
	if err != nil {
		h.logger.Error("failed to merge pull request", "pr_id", id, "error", err)
		respond.Error(w, http.StatusBadGateway, fmt.Errorf("merge pull request: %w", err))
		return
	}
	if !mergeResult.Merged {
		respond.Error(w, http.StatusConflict, errors.New(mergeResult.Message))
		return
	}

	now := time.Now().UTC()
	if _, err := h.db.ExecContext(ctx, `
		UPDATE pull_requests SET state = 'merged', merged_at = $1, updated_at = $1
		WHERE id = $2
	`, now, id); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("update pull request: %w", err))
		return
	}

	if _, err := h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'done', completed_at = $1, updated_at = $1
		WHERE id = $2
	`, now, taskID); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("update task: %w", err))
		return
	}

	if h.eventBus != nil {
		event := map[string]interface{}{
			"pr_id":      id,
			"task_id":    taskID,
			"pr_number":  pr.Number,
			"sha":        mergeResult.SHA,
			"timestamp":  now.Format(time.RFC3339),
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish(events.PRMerged, data); pubErr != nil {
			h.logger.Warn("failed to publish pr.merged event", "error", pubErr)
		}
	}

	pr.State = "merged"
	pr.MergedAt = &now
	pr.UpdatedAt = now

	respond.JSON(w, http.StatusOK, pr)
}

type verifiedCandidateForMerge struct {
	PullRequestID     string
	CandidateID       string
	CommitSHA         string
	CandidateTreeHash string
	EvidenceTreeHash  string
	ContractHash      string
	EnvironmentDigest string
	RunnerIdentity    string
	CompletedAt       time.Time
	PacketDigest      string
	Packet            json.RawMessage
}

func (v verifiedCandidateForMerge) Validate() error {
	if strings.TrimSpace(v.CommitSHA) == "" {
		return errors.New("verified candidate is missing commit sha")
	}
	if strings.TrimSpace(v.CandidateTreeHash) == "" || strings.TrimSpace(v.EvidenceTreeHash) == "" {
		return errors.New("verified candidate is missing tree identity")
	}
	if v.CandidateTreeHash != v.EvidenceTreeHash {
		return errors.New("verification evidence is stale for the candidate tree")
	}
	if strings.TrimSpace(v.ContractHash) == "" {
		return errors.New("verification evidence is missing contract identity")
	}
	if strings.TrimSpace(v.EnvironmentDigest) == "" || strings.TrimSpace(v.RunnerIdentity) == "" {
		return errors.New("verification evidence is missing runtime identity")
	}
	if v.CompletedAt.IsZero() {
		return errors.New("verification evidence is missing completion time")
	}
	if strings.TrimSpace(v.CandidateID) == "" || strings.TrimSpace(v.PacketDigest) == "" || len(v.Packet) == 0 {
		return errors.New("verified candidate is missing decision packet")
	}

	var packet decisionpacket.Packet
	if err := json.Unmarshal(v.Packet, &packet); err != nil {
		return fmt.Errorf("decode decision packet: %w", err)
	}
	if err := packet.Validate(); err != nil {
		return fmt.Errorf("invalid decision packet: %w", err)
	}
	digest, err := packet.Digest()
	if err != nil {
		return fmt.Errorf("digest decision packet: %w", err)
	}
	if digest != v.PacketDigest {
		return errors.New("decision packet digest does not match stored packet")
	}
	if packet.Candidate.ID != v.CandidateID ||
		packet.Candidate.PullRequestID != v.PullRequestID ||
		packet.Candidate.CommitSHA != v.CommitSHA ||
		packet.Candidate.TreeHash != v.CandidateTreeHash {
		return errors.New("decision packet is stale for the verified candidate")
	}
	if packet.Verification.ContractHash != v.ContractHash ||
		packet.Verification.EnvironmentDigest != v.EnvironmentDigest ||
		packet.Verification.RunnerIdentity != v.RunnerIdentity {
		return errors.New("decision packet verification identity does not match evidence")
	}
	if !packet.Review.Approvable {
		return errors.New("decision packet review is not approvable")
	}
	return nil
}

func (h *Handler) loadVerifiedCandidateForMerge(ctx context.Context, pullRequestID string) (*verifiedCandidateForMerge, error) {
	var verified verifiedCandidateForMerge
	err := h.db.QueryRowContext(ctx, `
		SELECT c.commit_sha, c.tree_hash, e.tree_hash, e.contract_hash,
		       e.environment_digest, e.runner_identity, e.completed_at,
		       c.id, dp.digest, dp.packet
		FROM change_candidates c
		JOIN verification_evidence e ON e.candidate_id = c.id
		JOIN decision_packets dp ON dp.candidate_id = c.id
		WHERE c.pull_request_id = $1
		ORDER BY e.completed_at DESC
		LIMIT 1
	`, pullRequestID).Scan(
		&verified.CommitSHA,
		&verified.CandidateTreeHash,
		&verified.EvidenceTreeHash,
		&verified.ContractHash,
		&verified.EnvironmentDigest,
		&verified.RunnerIdentity,
		&verified.CompletedAt,
		&verified.CandidateID,
		&verified.PacketDigest,
		&verified.Packet,
	)
	if err != nil {
		return nil, err
	}
	verified.PullRequestID = pullRequestID
	return &verified, nil
}
