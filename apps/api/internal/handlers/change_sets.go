package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/changegraph"
	"github.com/ai-dev-control-plane/changeset"
	"github.com/ai-dev-control-plane/decisionpacket"
)

type ChangeSet struct {
	ID                  string          `json:"id"`
	ProjectID           string          `json:"project_id"`
	Name                string          `json:"name"`
	Description         *string         `json:"description,omitempty"`
	Status              string          `json:"status"`
	PublicationDigest   *string         `json:"publication_digest,omitempty"`
	PublicationManifest json.RawMessage `json:"publication_manifest,omitempty"`
	AuthorizedAt        *time.Time      `json:"authorized_at,omitempty"`
	AuthorizedBy        *string         `json:"authorized_by,omitempty"`
	CreatedBy           string          `json:"created_by"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type CreateChangeSetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type AddChangeSetCandidateRequest struct {
	PullRequestID string `json:"pull_request_id"`
}

type ChangeSetStatusResponse struct {
	ChangeSet        ChangeSet           `json:"change_set"`
	Members          []changeset.Member  `json:"members"`
	Ready            bool                `json:"ready"`
	Blockers         []changeset.Blocker `json:"blockers,omitempty"`
	PublicationOrder []string            `json:"publication_order"`
}

func (h *Handler) CreateChangeSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}
	if err := authz.AuthorizeProject(ctx, h.db, user, projectID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("project not found"))
		return
	}

	var req CreateChangeSetRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}

	id := uuid.New().String()
	now := time.Now().UTC()
	var description any
	if strings.TrimSpace(req.Description) != "" {
		description = strings.TrimSpace(req.Description)
	}
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO change_sets (
			id, project_id, name, description, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, projectID, req.Name, description, "draft", user.UserID, now, now); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	changeSet := ChangeSet{
		ID: id, ProjectID: projectID, Name: req.Name, Status: "draft",
		CreatedBy: user.UserID, CreatedAt: now, UpdatedAt: now,
	}
	if description != nil {
		value := description.(string)
		changeSet.Description = &value
	}
	respond.JSON(w, http.StatusCreated, changeSet)
}

func (h *Handler) AddChangeSetCandidate(w http.ResponseWriter, r *http.Request) {
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
	if changeSet.Status != "draft" {
		respond.Error(w, http.StatusConflict, errors.New("change set membership is immutable after publication authorization"))
		return
	}

	var req AddChangeSetCandidateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}
	req.PullRequestID = strings.TrimSpace(req.PullRequestID)
	if req.PullRequestID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull_request_id is required"))
		return
	}
	if err := authz.AuthorizePullRequest(ctx, h.db, user, req.PullRequestID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}

	var candidateID, projectID, state string
	err = h.db.QueryRowContext(ctx, `
		SELECT c.id, t.project_id, pr.state
		FROM change_candidates c
		JOIN tasks t ON t.id = c.task_id
		JOIN pull_requests pr ON pr.id = c.pull_request_id
		WHERE c.pull_request_id = $1
	`, req.PullRequestID).Scan(&candidateID, &projectID, &state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusConflict, errors.New("pull request has no verified candidate"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if projectID != changeSet.ProjectID {
		respond.Error(w, http.StatusConflict, errors.New("candidate belongs to a different project"))
		return
	}
	if state != "open" {
		respond.Error(w, http.StatusConflict, errors.New("only open candidates can be added to a draft change set"))
		return
	}
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO change_set_candidates (change_set_id, candidate_id, added_at)
		VALUES ($1, $2, $3)
	`, changeSet.ID, candidateID, time.Now().UTC()); err != nil {
		respond.Error(w, http.StatusConflict, fmt.Errorf("add change set candidate: %w", err))
		return
	}
	respond.JSON(w, http.StatusCreated, map[string]string{
		"change_set_id": changeSet.ID,
		"candidate_id":  candidateID,
	})
}

func (h *Handler) GetChangeSet(w http.ResponseWriter, r *http.Request) {
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

	status, err := h.evaluateChangeSet(ctx, changeSet)
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	respond.JSON(w, http.StatusOK, status)
}

func (h *Handler) AuthorizeChangeSetPublication(w http.ResponseWriter, r *http.Request) {
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
	if changeSet.Status != "draft" {
		respond.Error(w, http.StatusConflict, fmt.Errorf("change set must be draft, current status: %s", changeSet.Status))
		return
	}

	status, graph, err := h.evaluateChangeSetWithGraph(ctx, changeSet)
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	if !status.Ready {
		respond.JSON(w, http.StatusConflict, map[string]any{
			"error":             "change set is not ready for publication",
			"blockers":          status.Blockers,
			"publication_order": status.PublicationOrder,
		})
		return
	}

	manifest, err := changeset.NewManifest(changeset.ManifestInput{
		ChangeSetID: changeSet.ID,
		ProjectID:   changeSet.ProjectID,
		Members:     status.Members,
		Graph:       graph,
	})
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	digest, err := manifest.Digest()
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	raw, err := manifest.Marshal()
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}
	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE change_sets
		SET status = 'authorized',
		    publication_digest = $1,
		    publication_manifest = $2,
		    authorized_at = $3,
		    authorized_by = $4,
		    updated_at = $3
		WHERE id = $5 AND status = 'draft'
	`, digest, string(raw), now, user.UserID, changeSet.ID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		respond.Error(w, http.StatusConflict, errors.New("change set status changed before publication authorization"))
		return
	}

	changeSet.Status = "authorized"
	changeSet.PublicationDigest = &digest
	changeSet.PublicationManifest = raw
	changeSet.AuthorizedAt = &now
	changeSet.AuthorizedBy = &user.UserID
	changeSet.UpdatedAt = now
	status.ChangeSet = *changeSet

	respond.JSON(w, http.StatusOK, status)
}

func (h *Handler) validateCandidateChangeSetPublication(ctx context.Context, candidateID, projectID string) error {
	var (
		changeSetID         string
		storedProjectID     string
		status              string
		publicationDigest   sql.NullString
		publicationManifest sql.NullString
	)
	err := h.db.QueryRowContext(ctx, `
		SELECT cs.id, cs.project_id, cs.status, cs.publication_digest, cs.publication_manifest
		FROM change_set_candidates csc
		JOIN change_sets cs ON cs.id = csc.change_set_id
		WHERE csc.candidate_id = $1
		LIMIT 1
	`, candidateID).Scan(
		&changeSetID, &storedProjectID, &status, &publicationDigest, &publicationManifest,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if storedProjectID != projectID {
		return errors.New("candidate change set project identity is inconsistent")
	}
	if status != "authorized" {
		return fmt.Errorf("candidate belongs to change set %s that is not authorized for publication", changeSetID)
	}
	if !publicationDigest.Valid || strings.TrimSpace(publicationDigest.String) == "" ||
		!publicationManifest.Valid || strings.TrimSpace(publicationManifest.String) == "" {
		return errors.New("authorized change set is missing publication authority")
	}

	members, err := h.loadChangeSetMembers(ctx, changeSetID)
	if err != nil {
		return fmt.Errorf("load authorized change set members: %w", err)
	}
	graph, _, _, err := h.loadProjectChangeGraph(ctx, projectID)
	if err != nil {
		return fmt.Errorf("load authorized change graph: %w", err)
	}
	current, err := changeset.NewManifest(changeset.ManifestInput{
		ChangeSetID: changeSetID,
		ProjectID:   projectID,
		Members:     members,
		Graph:       graph,
	})
	if err != nil {
		return fmt.Errorf("build current change set manifest: %w", err)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return fmt.Errorf("digest current change set manifest: %w", err)
	}
	if currentDigest != publicationDigest.String {
		return errors.New("change set publication authority is stale")
	}

	var stored changeset.Manifest
	if err := json.Unmarshal([]byte(publicationManifest.String), &stored); err != nil {
		return fmt.Errorf("decode stored change set manifest: %w", err)
	}
	storedDigest, err := stored.Digest()
	if err != nil {
		return fmt.Errorf("validate stored change set manifest: %w", err)
	}
	if storedDigest != publicationDigest.String ||
		stored.ChangeSetID != changeSetID ||
		stored.ProjectID != projectID {
		return errors.New("change set publication manifest integrity check failed")
	}
	return nil
}

func (h *Handler) loadChangeSet(ctx context.Context, id string) (*ChangeSet, error) {
	var (
		changeSet           ChangeSet
		description         sql.NullString
		publicationDigest   sql.NullString
		publicationManifest sql.NullString
		authorizedAt        sql.NullTime
		authorizedBy        sql.NullString
	)
	err := h.db.QueryRowContext(ctx, `
		SELECT id, project_id, name, description, status, publication_digest,
		       publication_manifest, authorized_at, authorized_by, created_by, created_at, updated_at
		FROM change_sets
		WHERE id = $1
	`, id).Scan(
		&changeSet.ID, &changeSet.ProjectID, &changeSet.Name, &description, &changeSet.Status,
		&publicationDigest, &publicationManifest, &authorizedAt, &authorizedBy,
		&changeSet.CreatedBy, &changeSet.CreatedAt, &changeSet.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if description.Valid {
		changeSet.Description = &description.String
	}
	if publicationDigest.Valid {
		changeSet.PublicationDigest = &publicationDigest.String
	}
	if publicationManifest.Valid {
		changeSet.PublicationManifest = json.RawMessage(publicationManifest.String)
	}
	if authorizedAt.Valid {
		changeSet.AuthorizedAt = &authorizedAt.Time
	}
	if authorizedBy.Valid {
		changeSet.AuthorizedBy = &authorizedBy.String
	}
	return &changeSet, nil
}

func (h *Handler) evaluateChangeSet(ctx context.Context, changeSet *ChangeSet) (*ChangeSetStatusResponse, error) {
	status, _, err := h.evaluateChangeSetWithGraph(ctx, changeSet)
	return status, err
}

func (h *Handler) evaluateChangeSetWithGraph(ctx context.Context, changeSet *ChangeSet) (*ChangeSetStatusResponse, changegraph.Graph, error) {
	members, err := h.loadChangeSetMembers(ctx, changeSet.ID)
	if err != nil {
		return nil, changegraph.Graph{}, err
	}
	graph, states, _, err := h.loadProjectChangeGraph(ctx, changeSet.ProjectID)
	if err != nil {
		return nil, changegraph.Graph{}, err
	}
	result, err := changeset.Evaluate(changeset.Input{Members: members, Graph: graph, States: states})
	if err != nil {
		return nil, changegraph.Graph{}, err
	}
	return &ChangeSetStatusResponse{
		ChangeSet:        *changeSet,
		Members:          members,
		Ready:            result.Ready,
		Blockers:         result.Blockers,
		PublicationOrder: result.PublicationOrder,
	}, graph, nil
}

func (h *Handler) loadChangeSetMembers(ctx context.Context, changeSetID string) ([]changeset.Member, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash,
		       dp.digest, dp.packet, pr.state
		FROM change_set_candidates csc
		JOIN change_candidates c ON c.id = csc.candidate_id
		JOIN decision_packets dp ON dp.candidate_id = c.id
		JOIN pull_requests pr ON pr.id = c.pull_request_id
		WHERE csc.change_set_id = $1
		ORDER BY c.id ASC
	`, changeSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]changeset.Member, 0)
	for rows.Next() {
		var candidateID, pullRequestID, repositoryID, commitSHA, treeHash, storedDigest, rawPacket, state string
		if err := rows.Scan(
			&candidateID, &pullRequestID, &repositoryID, &commitSHA, &treeHash,
			&storedDigest, &rawPacket, &state,
		); err != nil {
			return nil, err
		}

		var packet decisionpacket.Packet
		if err := json.Unmarshal([]byte(rawPacket), &packet); err != nil {
			return nil, fmt.Errorf("decode decision packet for candidate %s: %w", candidateID, err)
		}
		if err := packet.Validate(); err != nil {
			return nil, fmt.Errorf("invalid decision packet for candidate %s: %w", candidateID, err)
		}
		digest, err := packet.Digest()
		if err != nil {
			return nil, fmt.Errorf("digest decision packet for candidate %s: %w", candidateID, err)
		}
		if digest != storedDigest ||
			packet.Candidate.ID != candidateID ||
			packet.Candidate.PullRequestID != pullRequestID ||
			packet.Candidate.RepositoryID != repositoryID ||
			packet.Candidate.CommitSHA != commitSHA ||
			packet.Candidate.TreeHash != treeHash {
			return nil, fmt.Errorf("decision packet integrity mismatch for candidate %s", candidateID)
		}
		members = append(members, changeset.Member{
			ID:             candidateID,
			CommitSHA:      commitSHA,
			TreeHash:       treeHash,
			DecisionDigest: storedDigest,
			Approvable:     packet.Review.Approvable,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, errors.New("change set requires at least one candidate")
	}
	return members, nil
}
