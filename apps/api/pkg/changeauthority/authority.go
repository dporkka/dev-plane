package changeauthority

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/changegraph"
	"github.com/ai-dev-control-plane/changeset"
	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

type Actor struct {
	UserID         string
	OrganizationID string
	Role           string
}

type Request struct {
	PullRequestID string
	Method        string
	SHA           string
}

type GitHubGateway interface {
	MergePR(ctx context.Context, token *oauth2.Token, owner, name string, number int, req gateway.MergePRRequest) (*gateway.MergePRResult, error)
}

type EventPublisher interface {
	Publish(subject string, data []byte) error
}

type Service struct {
	db            *sql.DB
	logger        *slog.Logger
	githubGateway GitHubGateway
	githubToken   string
	eventBus      EventPublisher
	kernel        *capability.Kernel
}

func New(db *sql.DB, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		db:     db,
		logger: logger,
		kernel: capability.NewKernel(nil, nil, nil, logger),
	}
}

func (s *Service) WithGitHubGateway(g GitHubGateway) *Service {
	s.githubGateway = g
	return s
}

func (s *Service) WithGitHubToken(token string) *Service {
	s.githubToken = token
	return s
}

func (s *Service) WithEventPublisher(pub EventPublisher) *Service {
	s.eventBus = pub
	return s
}

func (s *Service) WithCapabilityKernel(kernel *capability.Kernel) *Service {
	if kernel != nil {
		s.kernel = kernel
	}
	return s
}

type authorityError struct {
	status int
	err    error
}

func (e *authorityError) Error() string { return e.err.Error() }
func (e *authorityError) Unwrap() error { return e.err }

func statusError(status int, err error) error {
	if err == nil {
		err = errors.New(http.StatusText(status))
	}
	return &authorityError{status: status, err: err}
}

func StatusCode(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var target *authorityError
	if errors.As(err, &target) {
		return target.status
	}
	return http.StatusInternalServerError
}

type verifiedCandidate struct {
	PullRequestID     string
	CandidateID       string
	ProjectID         string
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

func (v verifiedCandidate) validate() error {
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

type graphNode struct {
	id        string
	dependsOn []string
}

func (s *Service) Merge(ctx context.Context, actor Actor, req Request) (*models.PullRequest, error) {
	if s.db == nil {
		return nil, statusError(http.StatusInternalServerError, errors.New("merge authority database is not configured"))
	}
	if strings.TrimSpace(req.PullRequestID) == "" {
		return nil, statusError(http.StatusBadRequest, errors.New("pull request id is required"))
	}
	if strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(actor.OrganizationID) == "" {
		return nil, statusError(http.StatusForbidden, errors.New("authenticated actor is required"))
	}

	var (
		pr          models.PullRequest
		taskID      string
		runID       sql.NullString
		mergedAt    sql.NullTime
		repoOwner   string
		repoName    string
		taskStatus  string
		projectOrg  string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT pr.id, pr.task_id, pr.run_id, pr.repository_id, pr.number, pr.title, pr.body,
		       pr.branch, pr.base_branch, pr.url, pr.state, pr.draft, pr.created_by, pr.merged_at,
		       pr.created_at, pr.updated_at, r.owner, r.name, t.status, p.organization_id
		FROM pull_requests pr
		JOIN tasks t ON t.id = pr.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN repositories r ON r.id = pr.repository_id
		WHERE pr.id = $1
	`, req.PullRequestID).Scan(
		&pr.ID, &taskID, &runID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Body,
		&pr.Branch, &pr.BaseBranch, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedBy,
		&mergedAt, &pr.CreatedAt, &pr.UpdatedAt, &repoOwner, &repoName, &taskStatus, &projectOrg,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, statusError(http.StatusNotFound, errors.New("pull request not found"))
		}
		return nil, statusError(http.StatusInternalServerError, err)
	}
	pr.TaskID = taskID
	if runID.Valid {
		pr.RunID = &runID.String
	}
	if mergedAt.Valid {
		pr.MergedAt = &mergedAt.Time
	}
	if projectOrg != actor.OrganizationID {
		return nil, statusError(http.StatusNotFound, errors.New("pull request not found"))
	}
	if pr.State == "merged" {
		return nil, statusError(http.StatusConflict, errors.New("pull request is already merged"))
	}
	if taskStatus != "pr_created" {
		return nil, statusError(http.StatusBadRequest, fmt.Errorf("task must be in 'pr_created' status, current: %s", taskStatus))
	}

	kernel := s.kernel
	if kernel == nil {
		kernel = capability.NewKernel(nil, nil, nil, s.logger)
	}
	result, err := kernel.Evaluate(ctx, capability.Request{
		ActorType: "human",
		User: &models.User{
			ID:             actor.UserID,
			OrganizationID: actor.OrganizationID,
			Role:           actor.Role,
		},
		Operation: capability.OpMergePR,
		Resource:  fmt.Sprintf("%s/%s#%d", repoOwner, repoName, pr.Number),
		Details: map[string]any{
			"organization_id": actor.OrganizationID,
			"pull_request_id": req.PullRequestID,
		},
	})
	if err != nil {
		return nil, statusError(http.StatusInternalServerError, fmt.Errorf("authorize merge: %w", err))
	}
	if result.Effect == policies.EffectDeny {
		return nil, statusError(http.StatusForbidden, errors.New(result.Reason))
	}
	if result.RequiredApproval {
		return nil, statusError(http.StatusLocked, errors.New(result.Reason))
	}

	token := strings.TrimSpace(s.githubToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		return nil, statusError(http.StatusServiceUnavailable, errors.New("github token is not configured"))
	}
	gh := s.githubGateway
	if gh == nil {
		gh = gateway.NewGitHubGateway(os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"))
	}

	verified, err := s.loadVerifiedCandidate(ctx, req.PullRequestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, statusError(http.StatusConflict, errors.New("pull request has no verified candidate evidence"))
		}
		return nil, statusError(http.StatusInternalServerError, fmt.Errorf("load verified candidate: %w", err))
	}
	if err := verified.validate(); err != nil {
		return nil, statusError(http.StatusConflict, err)
	}
	blockers, err := s.candidateDependencyBlockers(ctx, verified.ProjectID, verified.CandidateID)
	if err != nil {
		return nil, statusError(http.StatusConflict, fmt.Errorf("evaluate change graph: %w", err))
	}
	if len(blockers) > 0 {
		return nil, statusError(http.StatusConflict, fmt.Errorf("candidate dependencies are not merged: %s", strings.Join(blockers, ", ")))
	}
	if err := s.validateCandidateChangeSetPublication(ctx, verified.CandidateID, verified.ProjectID); err != nil {
		return nil, statusError(http.StatusConflict, err)
	}
	if req.SHA != "" && req.SHA != verified.CommitSHA {
		return nil, statusError(http.StatusConflict, errors.New("requested sha does not match verified candidate"))
	}

	mergeResult, err := gh.MergePR(ctx, &oauth2.Token{AccessToken: token}, repoOwner, repoName, pr.Number, gateway.MergePRRequest{
		Method: req.Method,
		SHA:    verified.CommitSHA,
	})
	if err != nil {
		s.logger.Error("failed to merge pull request", "pr_id", req.PullRequestID, "error", err)
		return nil, statusError(http.StatusBadGateway, fmt.Errorf("merge pull request: %w", err))
	}
	if !mergeResult.Merged {
		return nil, statusError(http.StatusConflict, errors.New(mergeResult.Message))
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE pull_requests SET state = 'merged', merged_at = $1, updated_at = $1
		WHERE id = $2
	`, now, req.PullRequestID); err != nil {
		return nil, statusError(http.StatusInternalServerError, fmt.Errorf("update pull request: %w", err))
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'done', completed_at = $1, updated_at = $1
		WHERE id = $2
	`, now, taskID); err != nil {
		return nil, statusError(http.StatusInternalServerError, fmt.Errorf("update task: %w", err))
	}

	if s.eventBus != nil {
		event := map[string]any{
			"pr_id":     req.PullRequestID,
			"task_id":   taskID,
			"pr_number": pr.Number,
			"sha":       mergeResult.SHA,
			"timestamp": now.Format(time.RFC3339),
		}
		data, _ := json.Marshal(event)
		if pubErr := s.eventBus.Publish(events.PRMerged, data); pubErr != nil {
			s.logger.Warn("failed to publish pr.merged event", "error", pubErr)
		}
	}

	pr.State = "merged"
	pr.MergedAt = &now
	pr.UpdatedAt = now
	return &pr, nil
}

func (s *Service) loadVerifiedCandidate(ctx context.Context, pullRequestID string) (*verifiedCandidate, error) {
	var verified verifiedCandidate
	var packet string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.commit_sha, c.tree_hash, e.tree_hash, e.contract_hash,
		       e.environment_digest, e.runner_identity, e.completed_at,
		       c.id, dp.digest, dp.packet, t.project_id
		FROM change_candidates c
		JOIN verification_evidence e ON e.candidate_id = c.id
		JOIN decision_packets dp ON dp.candidate_id = c.id
		JOIN tasks t ON t.id = c.task_id
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
		&packet,
		&verified.ProjectID,
	)
	if err != nil {
		return nil, err
	}
	verified.PullRequestID = pullRequestID
	verified.Packet = json.RawMessage(packet)
	return &verified, nil
}

func (s *Service) candidateDependencyBlockers(ctx context.Context, projectID, candidateID string) ([]string, error) {
	graph, states, err := s.loadProjectChangeGraph(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return changegraph.Blockers(graph, candidateID, states)
}

func (s *Service) loadProjectChangeGraph(ctx context.Context, projectID string) (changegraph.Graph, map[string]changegraph.State, error) {
	rows, err := s.db.QueryContext(ctx, `
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
		return changegraph.Graph{}, nil, err
	}
	defer rows.Close()

	nodes := make(map[string]*graphNode)
	order := make([]string, 0)
	states := make(map[string]changegraph.State)
	for rows.Next() {
		var candidateID, pullRequestID, repositoryID, commitSHA, treeHash, state string
		var dependencyID sql.NullString
		if err := rows.Scan(&candidateID, &pullRequestID, &repositoryID, &commitSHA, &treeHash, &state, &dependencyID); err != nil {
			return changegraph.Graph{}, nil, err
		}
		node, exists := nodes[candidateID]
		if !exists {
			node = &graphNode{id: candidateID}
			nodes[candidateID] = node
			order = append(order, candidateID)
			states[candidateID] = changegraph.State(state)
		}
		if dependencyID.Valid {
			node.dependsOn = append(node.dependsOn, dependencyID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return changegraph.Graph{}, nil, err
	}

	graph := changegraph.Graph{Nodes: make([]changegraph.Node, 0, len(order))}
	for _, id := range order {
		node := nodes[id]
		sort.Strings(node.dependsOn)
		graph.Nodes = append(graph.Nodes, changegraph.Node{
			ID:        id,
			DependsOn: append([]string(nil), node.dependsOn...),
		})
	}
	if err := changegraph.Validate(graph); err != nil {
		return changegraph.Graph{}, nil, fmt.Errorf("invalid persisted change graph: %w", err)
	}
	return graph, states, nil
}

func (s *Service) validateCandidateChangeSetPublication(ctx context.Context, candidateID, projectID string) error {
	var (
		changeSetID         string
		storedProjectID     string
		status              string
		publicationDigest   sql.NullString
		publicationManifest sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
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

	members, err := s.loadChangeSetMembers(ctx, changeSetID)
	if err != nil {
		return fmt.Errorf("load authorized change set members: %w", err)
	}
	graph, _, err := s.loadProjectChangeGraph(ctx, projectID)
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

func (s *Service) loadChangeSetMembers(ctx context.Context, changeSetID string) ([]changeset.Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash,
		       dp.digest, dp.packet, pr.state
		FROM change_set_candidates csc
		JOIN change_candidates c ON c.id = csc.candidate_id
		LEFT JOIN decision_packets dp ON dp.candidate_id = c.id
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
		var candidateID, pullRequestID, repositoryID, commitSHA, treeHash, state string
		var storedDigest, rawPacket sql.NullString
		if err := rows.Scan(
			&candidateID, &pullRequestID, &repositoryID, &commitSHA, &treeHash,
			&storedDigest, &rawPacket, &state,
		); err != nil {
			return nil, err
		}
		member := changeset.Member{
			ID:        candidateID,
			CommitSHA: commitSHA,
			TreeHash:  treeHash,
		}
		if !storedDigest.Valid || strings.TrimSpace(storedDigest.String) == "" ||
			!rawPacket.Valid || strings.TrimSpace(rawPacket.String) == "" {
			members = append(members, member)
			continue
		}

		var packet decisionpacket.Packet
		if err := json.Unmarshal([]byte(rawPacket.String), &packet); err != nil {
			return nil, fmt.Errorf("decode decision packet for candidate %s: %w", candidateID, err)
		}
		if err := packet.Validate(); err != nil {
			return nil, fmt.Errorf("invalid decision packet for candidate %s: %w", candidateID, err)
		}
		digest, err := packet.Digest()
		if err != nil {
			return nil, fmt.Errorf("digest decision packet for candidate %s: %w", candidateID, err)
		}
		if digest != storedDigest.String ||
			packet.Candidate.ID != candidateID ||
			packet.Candidate.PullRequestID != pullRequestID ||
			packet.Candidate.RepositoryID != repositoryID ||
			packet.Candidate.CommitSHA != commitSHA ||
			packet.Candidate.TreeHash != treeHash {
			return nil, fmt.Errorf("decision packet integrity mismatch for candidate %s", candidateID)
		}
		member.DecisionDigest = storedDigest.String
		member.Approvable = packet.Review.Approvable
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}
