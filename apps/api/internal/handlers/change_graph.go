package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/changegraph"
)

type candidateContext struct {
	ID        string
	ProjectID string
}

type ChangeGraphNodeResponse struct {
	CandidateID   string   `json:"candidate_id"`
	PullRequestID string   `json:"pull_request_id"`
	RepositoryID  string   `json:"repository_id"`
	CommitSHA     string   `json:"commit_sha"`
	TreeHash      string   `json:"tree_hash"`
	State         string   `json:"state"`
	DependsOn     []string `json:"depends_on,omitempty"`
}

type ChangeGraphResponse struct {
	TargetCandidateID string                    `json:"target_candidate_id"`
	Nodes             []ChangeGraphNodeResponse `json:"nodes"`
	Blockers          []string                  `json:"blockers,omitempty"`
	MergeReady        bool                      `json:"merge_ready"`
}

type AddCandidateDependencyRequest struct {
	DependsOnPullRequestID string `json:"depends_on_pull_request_id"`
}

type AddCandidateDependencyResponse struct {
	CandidateID          string `json:"candidate_id"`
	DependsOnCandidateID string `json:"depends_on_candidate_id"`
}

func (h *Handler) AddPullRequestDependency(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	prID := chi.URLParam(r, "id")
	if strings.TrimSpace(prID) == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id is required"))
		return
	}

	var req AddCandidateDependencyRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}
	req.DependsOnPullRequestID = strings.TrimSpace(req.DependsOnPullRequestID)
	if req.DependsOnPullRequestID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("depends_on_pull_request_id is required"))
		return
	}

	if err := authz.AuthorizePullRequest(ctx, h.db, user, prID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}
	if err := authz.AuthorizePullRequest(ctx, h.db, user, req.DependsOnPullRequestID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("dependency pull request not found"))
		return
	}

	candidate, err := h.loadCandidateContextByPR(ctx, prID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusConflict, errors.New("pull request has no verified candidate"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	dependency, err := h.loadCandidateContextByPR(ctx, req.DependsOnPullRequestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusConflict, errors.New("dependency pull request has no verified candidate"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if candidate.ProjectID != dependency.ProjectID {
		respond.Error(w, http.StatusConflict, errors.New("candidate dependencies must belong to the same project"))
		return
	}

	graph, _, _, err := h.loadProjectChangeGraph(ctx, candidate.ProjectID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	found := false
	for i := range graph.Nodes {
		if graph.Nodes[i].ID != candidate.ID {
			continue
		}
		graph.Nodes[i].DependsOn = append(graph.Nodes[i].DependsOn, dependency.ID)
		found = true
		break
	}
	if !found {
		respond.Error(w, http.StatusConflict, errors.New("candidate is not present in project change graph"))
		return
	}
	if err := changegraph.Validate(graph); err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}

	now := time.Now().UTC()
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO candidate_dependencies (candidate_id, depends_on_candidate_id, created_at)
		VALUES ($1, $2, $3)
	`, candidate.ID, dependency.ID, now); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusCreated, AddCandidateDependencyResponse{
		CandidateID:          candidate.ID,
		DependsOnCandidateID: dependency.ID,
	})
}

func (h *Handler) GetPullRequestChangeGraph(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	prID := chi.URLParam(r, "id")
	if strings.TrimSpace(prID) == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id is required"))
		return
	}
	if err := authz.AuthorizePullRequest(ctx, h.db, user, prID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
		return
	}

	candidate, err := h.loadCandidateContextByPR(ctx, prID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("verified candidate not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	graph, states, nodes, err := h.loadProjectChangeGraph(ctx, candidate.ProjectID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	blockers, err := changegraph.Blockers(graph, candidate.ID, states)
	if err != nil {
		respond.Error(w, http.StatusConflict, err)
		return
	}

	respond.JSON(w, http.StatusOK, ChangeGraphResponse{
		TargetCandidateID: candidate.ID,
		Nodes:             nodes,
		Blockers:          blockers,
		MergeReady:        len(blockers) == 0,
	})
}

func (h *Handler) candidateDependencyBlockers(ctx context.Context, projectID, candidateID string) ([]string, error) {
	graph, states, _, err := h.loadProjectChangeGraph(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return changegraph.Blockers(graph, candidateID, states)
}

func (h *Handler) loadCandidateContextByPR(ctx context.Context, pullRequestID string) (*candidateContext, error) {
	var candidate candidateContext
	err := h.db.QueryRowContext(ctx, `
		SELECT c.id, t.project_id
		FROM change_candidates c
		JOIN tasks t ON t.id = c.task_id
		WHERE c.pull_request_id = $1
	`, pullRequestID).Scan(&candidate.ID, &candidate.ProjectID)
	if err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (h *Handler) loadProjectChangeGraph(ctx context.Context, projectID string) (changegraph.Graph, map[string]changegraph.State, []ChangeGraphNodeResponse, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash,
		       pr.state, cd.depends_on_candidate_id
		FROM change_candidates c
		JOIN tasks t ON t.id = c.task_id
		JOIN pull_requests pr ON pr.id = c.pull_request_id
		LEFT JOIN candidate_dependencies cd ON cd.candidate_id = c.id
		WHERE t.project_id = $1
		ORDER BY c.created_at ASC, c.id ASC, cd.depends_on_candidate_id ASC
	`, projectID)
	if err != nil {
		return changegraph.Graph{}, nil, nil, err
	}
	defer rows.Close()

	nodeByID := make(map[string]*ChangeGraphNodeResponse)
	order := make([]string, 0)
	states := make(map[string]changegraph.State)
	for rows.Next() {
		var (
			candidateID, pullRequestID, repositoryID, commitSHA, treeHash, state string
			dependencyID                                                         sql.NullString
		)
		if err := rows.Scan(&candidateID, &pullRequestID, &repositoryID, &commitSHA, &treeHash, &state, &dependencyID); err != nil {
			return changegraph.Graph{}, nil, nil, err
		}
		node, exists := nodeByID[candidateID]
		if !exists {
			node = &ChangeGraphNodeResponse{
				CandidateID: candidateID, PullRequestID: pullRequestID, RepositoryID: repositoryID,
				CommitSHA: commitSHA, TreeHash: treeHash, State: state,
			}
			nodeByID[candidateID] = node
			order = append(order, candidateID)
			states[candidateID] = changegraph.State(state)
		}
		if dependencyID.Valid {
			node.DependsOn = append(node.DependsOn, dependencyID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return changegraph.Graph{}, nil, nil, err
	}

	nodes := make([]ChangeGraphNodeResponse, 0, len(order))
	graph := changegraph.Graph{Nodes: make([]changegraph.Node, 0, len(order))}
	for _, id := range order {
		node := nodeByID[id]
		sort.Strings(node.DependsOn)
		nodes = append(nodes, *node)
		graph.Nodes = append(graph.Nodes, changegraph.Node{ID: id, DependsOn: append([]string(nil), node.DependsOn...)})
	}
	if err := changegraph.Validate(graph); err != nil {
		return changegraph.Graph{}, nil, nil, fmt.Errorf("invalid persisted change graph: %w", err)
	}
	return graph, states, nodes, nil
}
 