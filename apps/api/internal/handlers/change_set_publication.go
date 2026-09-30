package handlers

import (
	"bytes"
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
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/changeset"
	"github.com/ai-dev-control-plane/gateway"
)

const publicationLeaseDuration = 5 * time.Minute

type githubPRReader interface {
	GetPR(ctx context.Context, token *oauth2.Token, owner, name string, number int) (*gateway.GitHubPR, error)
}

type changeSetPublicationMember struct {
	CandidateID   string
	Ordinal       int
	Status        string
	AttemptCount  int
	MergeSHA      *string
	LastError     *string
	PullRequestID string
	CommitSHA     string
	TaskID        string
	PRState       string
	PRNumber      int
	Owner         string
	RepoName      string
}

type ChangeSetPublicationMemberResponse struct {
	CandidateID  string  `json:"candidate_id"`
	Ordinal      int     `json:"ordinal"`
	Status       string  `json:"status"`
	AttemptCount int     `json:"attempt_count"`
	MergeSHA     *string `json:"merge_sha,omitempty"`
	LastError    *string `json:"last_error,omitempty"`
}

type ChangeSetPublicationResponse struct {
	ChangeSetID       string                               `json:"change_set_id"`
	PublicationStatus string                               `json:"publication_status"`
	Members           []ChangeSetPublicationMemberResponse `json:"members"`
}

func (h *Handler) PublishChangeSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("change set id is required"))
		return
	}

	changeSet, err := h.loadChangeSet(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("change set not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := authz.AuthorizeProject(ctx, h.db, user, changeSet.ProjectID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("change set not found"))
		return
	}

	if changeSet.Status == "completed" || changeSet.PublicationStatus == "completed" {
		members, err := h.loadChangeSetPublicationMembers(ctx, changeSet.ID)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		respond.JSON(w, http.StatusOK, publicationResponse(changeSet.ID, "completed", members))
		return
	}
	if changeSet.Status != "authorized" {
		respond.Error(w, http.StatusConflict, fmt.Errorf("change set must be authorized before publication, current status: %s", changeSet.Status))
		return
	}

	status, err := h.validateChangeSetPublicationAuthority(ctx, changeSet)
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	if !status.Ready {
		respond.JSON(w, http.StatusConflict, map[string]any{
			"error":    "change set publication authority is no longer ready",
			"blockers": status.Blockers,
		})
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
	reader, ok := gh.(githubPRReader)
	if !ok {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("github gateway does not support pull request reconciliation"))
		return
	}

	leaseToken := uuid.New().String()
	now := time.Now().UTC()
	leaseUntil := now.Add(publicationLeaseDuration)
	result, err := h.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = $1,
		    publication_lease_token = $2,
		    publication_lease_until = $3,
		    updated_at = $4
		WHERE id = $5
		  AND status = 'authorized'
		  AND publication_status <> 'completed'
		  AND (publication_lease_until IS NULL OR publication_lease_until < $6)
	`, "publishing", leaseToken, leaseUntil, now, changeSet.ID, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if affected != 1 {
		respond.Error(w, http.StatusConflict, errors.New("change set publication is already running or no longer publishable"))
		return
	}

	for ordinal, candidateID := range status.PublicationOrder {
		if _, err := h.db.ExecContext(ctx, `
			INSERT INTO change_set_publications (
				change_set_id, candidate_id, ordinal, status, updated_at
			) VALUES ($1, $2, $3, 'pending', $4)
			ON CONFLICT (change_set_id, candidate_id) DO NOTHING
		`, changeSet.ID, candidateID, ordinal, now); err != nil {
			h.failChangeSetPublication(ctx, changeSet.ID, leaseToken, "blocked", fmt.Sprintf("materialize publication plan: %v", err))
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
	}

	members, err := h.loadChangeSetPublicationMembers(ctx, changeSet.ID)
	if err != nil {
		h.failChangeSetPublication(ctx, changeSet.ID, leaseToken, "blocked", fmt.Sprintf("load publication plan: %v", err))
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	for i := range members {
		member := &members[i]
		if member.Status == "merged" {
			continue
		}

		attemptedAt := time.Now().UTC()
		if _, err := h.db.ExecContext(ctx, `
			UPDATE change_set_publications
			SET status = 'publishing',
			    attempt_count = attempt_count + 1,
			    last_error = NULL,
			    started_at = COALESCE(started_at, $1),
			    updated_at = $1
			WHERE change_set_id = $2 AND candidate_id = $3
		`, attemptedAt, changeSet.ID, member.CandidateID); err != nil {
			h.failChangeSetPublication(ctx, changeSet.ID, leaseToken, "blocked", fmt.Sprintf("checkpoint publication attempt: %v", err))
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		member.Status = "publishing"
		member.AttemptCount++

		remote, err := reader.GetPR(ctx, &oauth2.Token{AccessToken: token}, member.Owner, member.RepoName, member.PRNumber)
		if err != nil {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, fmt.Sprintf("reconcile github pull request: %v", err))
			return
		}
		if remote == nil {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, "github pull request reconciliation returned no result")
			return
		}
		if remote.Head.SHA != member.CommitSHA {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, "github pull request head does not match authorized candidate")
			return
		}

		if remote.Merged {
			if err := h.reconcilePublicationMemberMerged(ctx, changeSet.ID, member, remote.MergeCommitSHA); err != nil {
				h.failChangeSetPublication(ctx, changeSet.ID, leaseToken, "blocked", err.Error())
				respond.Error(w, http.StatusInternalServerError, err)
				return
			}
			member.Status = "merged"
			mergeSHA := remote.MergeCommitSHA
			member.MergeSHA = &mergeSHA
			continue
		}
		if remote.State != "open" {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, "github pull request is closed without merge")
			return
		}

		if err := h.invokeMergeAuthority(ctx, user, member.PullRequestID); err != nil {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, err.Error())
			return
		}

		remote, err = reader.GetPR(ctx, &oauth2.Token{AccessToken: token}, member.Owner, member.RepoName, member.PRNumber)
		if err != nil {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, fmt.Sprintf("confirm github merge: %v", err))
			return
		}
		if remote == nil || !remote.Merged {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, "github did not confirm merged state after merge authority completed")
			return
		}
		if remote.Head.SHA != member.CommitSHA {
			h.blockPublicationMember(w, ctx, changeSet.ID, leaseToken, member, "github pull request head changed after merge")
			return
		}
		if err := h.checkpointPublicationMemberMerged(ctx, changeSet.ID, member.CandidateID, remote.MergeCommitSHA); err != nil {
			h.failChangeSetPublication(ctx, changeSet.ID, leaseToken, "blocked", err.Error())
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		member.Status = "merged"
		mergeSHA := remote.MergeCommitSHA
		member.MergeSHA = &mergeSHA
	}

	completedAt := time.Now().UTC()
	result, err = h.db.ExecContext(ctx, `
		UPDATE change_sets
		SET status = $1,
		    publication_status = $2,
		    publication_lease_token = NULL,
		    publication_lease_until = NULL,
		    updated_at = $3
		WHERE id = $4 AND publication_lease_token = $5
	`, "completed", "completed", completedAt, changeSet.ID, leaseToken)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	affected, err = result.RowsAffected()
	if err != nil || affected != 1 {
		respond.Error(w, http.StatusConflict, errors.New("change set publication lease was lost before completion"))
		return
	}

	respond.JSON(w, http.StatusOK, publicationResponse(changeSet.ID, "completed", members))
}

func (h *Handler) validateChangeSetPublicationAuthority(ctx context.Context, changeSet *ChangeSet) (*ChangeSetStatusResponse, error) {
	if changeSet.PublicationDigest == nil || strings.TrimSpace(*changeSet.PublicationDigest) == "" ||
		len(changeSet.PublicationManifest) == 0 {
		return nil, errors.New("authorized change set is missing publication authority")
	}

	status, graph, err := h.evaluateChangeSetWithGraph(ctx, changeSet)
	if err != nil {
		return nil, err
	}
	current, err := changeset.NewManifest(changeset.ManifestInput{
		ChangeSetID: changeSet.ID,
		ProjectID:   changeSet.ProjectID,
		Members:     status.Members,
		Graph:       graph,
	})
	if err != nil {
		return nil, err
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, err
	}
	if currentDigest != *changeSet.PublicationDigest {
		return nil, errors.New("change set publication authority is stale")
	}

	var stored changeset.Manifest
	if err := json.Unmarshal(changeSet.PublicationManifest, &stored); err != nil {
		return nil, fmt.Errorf("decode stored change set publication manifest: %w", err)
	}
	storedDigest, err := stored.Digest()
	if err != nil {
		return nil, fmt.Errorf("validate stored change set publication manifest: %w", err)
	}
	if storedDigest != *changeSet.PublicationDigest ||
		stored.ChangeSetID != changeSet.ID ||
		stored.ProjectID != changeSet.ProjectID {
		return nil, errors.New("change set publication manifest integrity check failed")
	}
	return status, nil
}

func (h *Handler) loadChangeSetPublicationMembers(ctx context.Context, changeSetID string) ([]changeSetPublicationMember, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT cp.candidate_id, cp.ordinal, cp.status, cp.attempt_count, cp.merge_sha, cp.last_error,
		       c.pull_request_id, c.commit_sha, c.task_id, pr.state, pr.number, r.owner, r.name
		FROM change_set_publications cp
		JOIN change_candidates c ON c.id = cp.candidate_id
		JOIN pull_requests pr ON pr.id = c.pull_request_id
		JOIN repositories r ON r.id = c.repository_id
		WHERE cp.change_set_id = $1
		ORDER BY cp.ordinal ASC
	`, changeSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]changeSetPublicationMember, 0)
	for rows.Next() {
		var member changeSetPublicationMember
		var mergeSHA, lastError sql.NullString
		if err := rows.Scan(
			&member.CandidateID, &member.Ordinal, &member.Status, &member.AttemptCount,
			&mergeSHA, &lastError, &member.PullRequestID, &member.CommitSHA,
			&member.TaskID, &member.PRState, &member.PRNumber, &member.Owner, &member.RepoName,
		); err != nil {
			return nil, err
		}
		if mergeSHA.Valid {
			member.MergeSHA = &mergeSHA.String
		}
		if lastError.Valid {
			member.LastError = &lastError.String
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}

func (h *Handler) reconcilePublicationMemberMerged(ctx context.Context, changeSetID string, member *changeSetPublicationMember, mergeSHA string) error {
	now := time.Now().UTC()
	if _, err := h.db.ExecContext(ctx, `
		UPDATE pull_requests SET state = 'merged', merged_at = COALESCE(merged_at, $1), updated_at = $1
		WHERE id = $2
	`, now, member.PullRequestID); err != nil {
		return fmt.Errorf("reconcile local pull request: %w", err)
	}
	if _, err := h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'done', completed_at = COALESCE(completed_at, $1), updated_at = $1
		WHERE id = $2
	`, now, member.TaskID); err != nil {
		return fmt.Errorf("reconcile local task: %w", err)
	}
	return h.checkpointPublicationMemberMerged(ctx, changeSetID, member.CandidateID, mergeSHA)
}

func (h *Handler) checkpointPublicationMemberMerged(ctx context.Context, changeSetID, candidateID, mergeSHA string) error {
	now := time.Now().UTC()
	if _, err := h.db.ExecContext(ctx, `
		UPDATE change_set_publications SET status = 'merged',
		    merge_sha = $1, last_error = NULL, completed_at = $2, updated_at = $2
		WHERE change_set_id = $3 AND candidate_id = $4
	`, mergeSHA, now, changeSetID, candidateID); err != nil {
		return fmt.Errorf("checkpoint merged publication member: %w", err)
	}
	return nil
}

func (h *Handler) blockPublicationMember(w http.ResponseWriter, ctx context.Context, changeSetID, leaseToken string, member *changeSetPublicationMember, reason string) {
	now := time.Now().UTC()
	_, _ = h.db.ExecContext(ctx, `
		UPDATE change_set_publications SET status = 'blocked', last_error = $1, updated_at = $2
		WHERE change_set_id = $3 AND candidate_id = $4
	`, reason, now, changeSetID, member.CandidateID)
	_, _ = h.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = $1,
		    publication_lease_token = NULL,
		    publication_lease_until = NULL,
		    updated_at = $2
		WHERE id = $3 AND publication_lease_token = $4
	`, "blocked", now, changeSetID, leaseToken)
	member.Status = "blocked"
	member.LastError = &reason
	respond.JSON(w, http.StatusConflict, map[string]any{
		"error":              reason,
		"candidate_id":       member.CandidateID,
		"publication_status": "blocked",
	})
}

func (h *Handler) failChangeSetPublication(ctx context.Context, changeSetID, leaseToken, status, reason string) {
	now := time.Now().UTC()
	_, _ = h.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = $1,
		    publication_lease_token = NULL,
		    publication_lease_until = NULL,
		    updated_at = $2
		WHERE id = $3 AND publication_lease_token = $4
	`, status, now, changeSetID, leaseToken)
}

func publicationResponse(changeSetID, publicationStatus string, members []changeSetPublicationMember) ChangeSetPublicationResponse {
	responseMembers := make([]ChangeSetPublicationMemberResponse, 0, len(members))
	for _, member := range members {
		responseMembers = append(responseMembers, ChangeSetPublicationMemberResponse{
			CandidateID:  member.CandidateID,
			Ordinal:      member.Ordinal,
			Status:       member.Status,
			AttemptCount: member.AttemptCount,
			MergeSHA:     member.MergeSHA,
			LastError:    member.LastError,
		})
	}
	return ChangeSetPublicationResponse{
		ChangeSetID:       changeSetID,
		PublicationStatus: publicationStatus,
		Members:           responseMembers,
	}
}

type mergeAuthorityCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newMergeAuthorityCapture() *mergeAuthorityCapture {
	return &mergeAuthorityCapture{header: make(http.Header)}
}

func (c *mergeAuthorityCapture) Header() http.Header {
	return c.header
}

func (c *mergeAuthorityCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *mergeAuthorityCapture) Write(data []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(data)
}

func (h *Handler) invokeMergeAuthority(ctx context.Context, user *auth.Claims, pullRequestID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/pull-requests/"+pullRequestID+"/merge", strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("build merge authority request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pullRequestID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	capture := newMergeAuthorityCapture()
	h.MergePullRequest(capture, req)
	if capture.status >= 200 && capture.status < 300 {
		return nil
	}
	message := strings.TrimSpace(capture.body.String())
	if message == "" {
		message = http.StatusText(capture.status)
	}
	return fmt.Errorf("merge authority rejected pull request %s: %s", pullRequestID, message)
}
