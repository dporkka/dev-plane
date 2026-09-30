package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
)

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

func (h *Handler) GetChangeSetPublication(w http.ResponseWriter, r *http.Request) {
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

	members, err := h.loadChangeSetPublicationMembers(ctx, changeSet.ID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusOK, publicationResponse(changeSet.ID, changeSet.PublicationStatus, members))
}

// PublishChangeSet requests asynchronous publication by the worker. All
// authority validation, lease acquisition, GitHub reconciliation, and merge
// execution occur in the durable worker-side publisher.
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
	if h.eventBus == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("change set publication worker is unavailable"))
		return
	}

	event := events.ChangeSetPublicationEvent{
		ChangeSetID:    changeSet.ID,
		ProjectID:      changeSet.ProjectID,
		ActorID:        user.UserID,
		OrganizationID: user.OrgID,
		Status:         "requested",
	}
	data, err := json.Marshal(event)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("marshal change set publication request: %w", err))
		return
	}
	if err := h.eventBus.Publish(events.ChangeSetPublishRequested, data); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, fmt.Errorf("queue change set publication: %w", err))
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]any{
		"change_set_id":       changeSet.ID,
		"publication_status": changeSet.PublicationStatus,
		"queued":             true,
	})
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
