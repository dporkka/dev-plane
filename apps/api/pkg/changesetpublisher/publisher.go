package changesetpublisher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/pkg/changeauthority"
	"github.com/ai-dev-control-plane/changegraph"
	"github.com/ai-dev-control-plane/changeset"
	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

const defaultLeaseDuration = 5 * time.Minute

type GitHubReader interface {
	GetPR(ctx context.Context, token *oauth2.Token, owner, name string, number int) (*gateway.GitHubPR, error)
}

type MergeAuthority interface {
	Merge(ctx context.Context, actor changeauthority.Actor, req changeauthority.Request) (*models.PullRequest, error)
}

type EventPublisher interface {
	Publish(subject string, data []byte) error
}

type ProgressFunc func() error

type Publisher struct {
	db            *sql.DB
	logger        *slog.Logger
	reader        GitHubReader
	merger        MergeAuthority
	githubToken   string
	eventBus      EventPublisher
	leaseDuration time.Duration
}

type MemberResult struct {
	CandidateID  string  `json:"candidate_id"`
	Ordinal      int     `json:"ordinal"`
	Status       string  `json:"status"`
	AttemptCount int     `json:"attempt_count"`
	MergeSHA     *string `json:"merge_sha,omitempty"`
	LastError    *string `json:"last_error,omitempty"`
}

type Result struct {
	ChangeSetID       string         `json:"change_set_id"`
	PublicationStatus string         `json:"publication_status"`
	Members           []MemberResult `json:"members"`
}

type ErrorKind string

const (
	ErrorBlocked        ErrorKind = "blocked"
	ErrorRetryable      ErrorKind = "retryable"
	ErrorAlreadyRunning ErrorKind = "already_running"
	ErrorInvalid        ErrorKind = "invalid"
)

type PublishError struct {
	Kind        ErrorKind
	CandidateID string
	Err         error
}

func (e *PublishError) Error() string {
	if e == nil || e.Err == nil {
		return string(e.Kind)
	}
	return e.Err.Error()
}

func (e *PublishError) Unwrap() error { return e.Err }

func ErrorKindOf(err error) ErrorKind {
	var target *PublishError
	if errors.As(err, &target) {
		return target.Kind
	}
	return ErrorRetryable
}

func IsRetryable(err error) bool {
	return ErrorKindOf(err) == ErrorRetryable
}

func New(db *sql.DB, logger *slog.Logger) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{
		db:            db,
		logger:        logger,
		leaseDuration: defaultLeaseDuration,
	}
}

func (p *Publisher) WithGitHubReader(reader GitHubReader) *Publisher {
	p.reader = reader
	return p
}

func (p *Publisher) WithMergeAuthority(merger MergeAuthority) *Publisher {
	p.merger = merger
	return p
}

func (p *Publisher) WithGitHubToken(token string) *Publisher {
	p.githubToken = token
	return p
}

func (p *Publisher) WithEventPublisher(eventBus EventPublisher) *Publisher {
	p.eventBus = eventBus
	return p
}

func (p *Publisher) WithLeaseDuration(duration time.Duration) *Publisher {
	if duration > 0 {
		p.leaseDuration = duration
	}
	return p
}

type changeSetRecord struct {
	ID                  string
	ProjectID           string
	Status              string
	PublicationStatus   string
	PublicationDigest   string
	PublicationManifest json.RawMessage
	OrganizationID      string
}

type publicationMember struct {
	CandidateID   string
	Ordinal       int
	Status        string
	AttemptCount  int
	MergeSHA      *string
	LastError     *string
	PullRequestID string
	CommitSHA     string
	TaskID        string
	PRNumber      int
	Owner         string
	RepoName      string
}

type graphNode struct {
	id        string
	dependsOn []string
}

func (p *Publisher) Publish(ctx context.Context, changeSetID string, actor changeauthority.Actor, progress ProgressFunc) (*Result, error) {
	if p.db == nil {
		return nil, &PublishError{Kind: ErrorRetryable, Err: errors.New("publication database is not configured")}
	}
	changeSetID = strings.TrimSpace(changeSetID)
	if changeSetID == "" {
		return nil, &PublishError{Kind: ErrorInvalid, Err: errors.New("change set id is required")}
	}

	changeSet, err := p.loadChangeSet(ctx, changeSetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &PublishError{Kind: ErrorInvalid, Err: errors.New("change set not found")}
		}
		return nil, &PublishError{Kind: ErrorRetryable, Err: err}
	}
	if changeSet.OrganizationID != actor.OrganizationID {
		return nil, &PublishError{Kind: ErrorInvalid, Err: errors.New("change set not found")}
	}
	if changeSet.Status == "completed" || changeSet.PublicationStatus == "completed" {
		members, err := p.loadPublicationMembers(ctx, changeSet.ID)
		if err != nil {
			return nil, &PublishError{Kind: ErrorRetryable, Err: err}
		}
		p.publishLifecycle(events.ChangeSetPublicationCompleted, changeSet, actor, "", "")
		return publicationResult(changeSet.ID, "completed", members), nil
	}
	if changeSet.Status != "authorized" {
		return nil, &PublishError{Kind: ErrorInvalid, Err: fmt.Errorf("change set must be authorized before publication, current status: %s", changeSet.Status)}
	}

	members, graph, evaluation, err := p.validateAuthority(ctx, changeSet)
	if err != nil {
		p.markSetBlocked(ctx, changeSet.ID)
		p.publishLifecycle(events.ChangeSetPublicationBlocked, changeSet, actor, "", err.Error())
		return nil, &PublishError{Kind: ErrorBlocked, Err: err}
	}
	if !evaluation.Ready {
		err := fmt.Errorf("change set publication authority is not ready: %s", formatBlockers(evaluation.Blockers))
		p.markSetBlocked(ctx, changeSet.ID)
		p.publishLifecycle(events.ChangeSetPublicationBlocked, changeSet, actor, "", err.Error())
		return nil, &PublishError{Kind: ErrorBlocked, Err: err}
	}
	_ = graph
	_ = members

	token := strings.TrimSpace(p.githubToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		return nil, &PublishError{Kind: ErrorRetryable, Err: errors.New("github token is not configured")}
	}

	reader := p.reader
	var gh *gateway.GitHubGateway
	if reader == nil || p.merger == nil {
		gh = gateway.NewGitHubGateway(os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"))
	}
	if reader == nil {
		reader = gh
	}
	merger := p.merger
	if merger == nil {
		merger = changeauthority.New(p.db, p.logger).
			WithGitHubGateway(gh).
			WithGitHubToken(token).
			WithEventPublisher(p.eventBus)
	}

	leaseToken := uuid.New().String()
	now := time.Now().UTC()
	leaseUntil := now.Add(p.leaseDuration)
	result, err := p.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = 'publishing',
		    publication_lease_token = $1,
		    publication_lease_until = $2,
		    updated_at = $3
		WHERE id = $4
		  AND status = 'authorized'
		  AND publication_status <> 'completed'
		  AND (publication_lease_until IS NULL OR publication_lease_until < $5)
	`, leaseToken, leaseUntil, now, changeSet.ID, now)
	if err != nil {
		return nil, &PublishError{Kind: ErrorRetryable, Err: err}
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, &PublishError{Kind: ErrorRetryable, Err: err}
	}
	if affected != 1 {
		return nil, &PublishError{Kind: ErrorAlreadyRunning, Err: errors.New("change set publication is already running or no longer publishable")}
	}

	p.publishLifecycle(events.ChangeSetPublicationStarted, changeSet, actor, "", "")

	for ordinal, candidateID := range evaluation.PublicationOrder {
		if _, err := p.db.ExecContext(ctx, `
			INSERT INTO change_set_publications (
				change_set_id, candidate_id, ordinal, status, updated_at
			) VALUES ($1, $2, $3, 'pending', $4)
			ON CONFLICT (change_set_id, candidate_id) DO NOTHING
		`, changeSet.ID, candidateID, ordinal, now); err != nil {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, "", fmt.Errorf("materialize publication plan: %w", err))
		}
	}

	publicationMembers, err := p.loadPublicationMembers(ctx, changeSet.ID)
	if err != nil {
		return nil, p.transientFailure(ctx, changeSet, leaseToken, "", fmt.Errorf("load publication plan: %w", err))
	}

	for i := range publicationMembers {
		member := &publicationMembers[i]
		if member.Status == "merged" {
			continue
		}

		if err := p.renewLease(ctx, changeSet.ID, leaseToken); err != nil {
			return nil, &PublishError{Kind: ErrorRetryable, CandidateID: member.CandidateID, Err: err}
		}
		if progress != nil {
			if err := progress(); err != nil {
				return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, fmt.Errorf("publication progress heartbeat: %w", err))
			}
		}

		attemptedAt := time.Now().UTC()
		if _, err := p.db.ExecContext(ctx, `
			UPDATE change_set_publications
			SET status = 'publishing',
			    attempt_count = attempt_count + 1,
			    last_error = NULL,
			    started_at = COALESCE(started_at, $1),
			    updated_at = $1
			WHERE change_set_id = $2 AND candidate_id = $3
		`, attemptedAt, changeSet.ID, member.CandidateID); err != nil {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, fmt.Errorf("checkpoint publication attempt: %w", err))
		}
		member.Status = "publishing"
		member.AttemptCount++

		remote, err := reader.GetPR(ctx, &oauth2.Token{AccessToken: token}, member.Owner, member.RepoName, member.PRNumber)
		if err != nil {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, fmt.Errorf("reconcile github pull request: %w", err))
		}
		if remote == nil {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, errors.New("github pull request reconciliation returned no result"))
		}
		if remote.Head.SHA != member.CommitSHA {
			return nil, p.blockFailure(ctx, changeSet, leaseToken, actor, member.CandidateID, errors.New("github pull request head does not match authorized candidate"))
		}

		if remote.Merged {
			if err := p.reconcileMerged(ctx, changeSet.ID, member, remote.MergeCommitSHA); err != nil {
				return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, err)
			}
			member.Status = "merged"
			mergeSHA := remote.MergeCommitSHA
			member.MergeSHA = &mergeSHA
			continue
		}
		if remote.State != "open" {
			return nil, p.blockFailure(ctx, changeSet, leaseToken, actor, member.CandidateID, errors.New("github pull request is closed without merge"))
		}

		if _, err := merger.Merge(ctx, actor, changeauthority.Request{PullRequestID: member.PullRequestID}); err != nil {
			if changeauthority.StatusCode(err) >= 500 {
				return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, fmt.Errorf("merge authority: %w", err))
			}
			return nil, p.blockFailure(ctx, changeSet, leaseToken, actor, member.CandidateID, fmt.Errorf("merge authority: %w", err))
		}

		if err := p.renewLease(ctx, changeSet.ID, leaseToken); err != nil {
			return nil, &PublishError{Kind: ErrorRetryable, CandidateID: member.CandidateID, Err: err}
		}
		if progress != nil {
			if err := progress(); err != nil {
				return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, fmt.Errorf("publication progress heartbeat: %w", err))
			}
		}

		remote, err = reader.GetPR(ctx, &oauth2.Token{AccessToken: token}, member.Owner, member.RepoName, member.PRNumber)
		if err != nil {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, fmt.Errorf("confirm github merge: %w", err))
		}
		if remote == nil || !remote.Merged {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, errors.New("github did not confirm merged state after merge authority completed"))
		}
		if remote.Head.SHA != member.CommitSHA {
			return nil, p.blockFailure(ctx, changeSet, leaseToken, actor, member.CandidateID, errors.New("github pull request head changed after merge"))
		}
		if err := p.checkpointMerged(ctx, changeSet.ID, member.CandidateID, remote.MergeCommitSHA); err != nil {
			return nil, p.transientFailure(ctx, changeSet, leaseToken, member.CandidateID, err)
		}
		member.Status = "merged"
		mergeSHA := remote.MergeCommitSHA
		member.MergeSHA = &mergeSHA
	}

	completedAt := time.Now().UTC()
	result, err = p.db.ExecContext(ctx, `
		UPDATE change_sets
		SET status = 'completed',
		    publication_status = 'completed',
		    publication_lease_token = NULL,
		    publication_lease_until = NULL,
		    updated_at = $1
		WHERE id = $2 AND publication_lease_token = $3
	`, completedAt, changeSet.ID, leaseToken)
	if err != nil {
		return nil, &PublishError{Kind: ErrorRetryable, Err: err}
	}
	affected, err = result.RowsAffected()
	if err != nil || affected != 1 {
		return nil, &PublishError{Kind: ErrorRetryable, Err: errors.New("change set publication lease was lost before completion")}
	}

	p.publishLifecycle(events.ChangeSetPublicationCompleted, changeSet, actor, "", "")
	return publicationResult(changeSet.ID, "completed", publicationMembers), nil
}

func (p *Publisher) loadChangeSet(ctx context.Context, id string) (*changeSetRecord, error) {
	var record changeSetRecord
	var digest, manifest sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT cs.id, cs.project_id, cs.status, cs.publication_status,
		       cs.publication_digest, cs.publication_manifest, p.organization_id
		FROM change_sets cs
		JOIN projects p ON p.id = cs.project_id
		WHERE cs.id = $1
	`, id).Scan(
		&record.ID, &record.ProjectID, &record.Status, &record.PublicationStatus,
		&digest, &manifest, &record.OrganizationID,
	)
	if err != nil {
		return nil, err
	}
	if digest.Valid {
		record.PublicationDigest = digest.String
	}
	if manifest.Valid {
		record.PublicationManifest = json.RawMessage(manifest.String)
	}
	return &record, nil
}

func (p *Publisher) validateAuthority(ctx context.Context, record *changeSetRecord) ([]changeset.Member, changegraph.Graph, changeset.Result, error) {
	if strings.TrimSpace(record.PublicationDigest) == "" || len(record.PublicationManifest) == 0 {
		return nil, changegraph.Graph{}, changeset.Result{}, errors.New("authorized change set is missing publication authority")
	}
	members, err := p.loadChangeSetMembers(ctx, record.ID)
	if err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, err
	}
	graph, states, err := p.loadProjectGraph(ctx, record.ProjectID)
	if err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, err
	}
	evaluation, err := changeset.Evaluate(changeset.Input{Members: members, Graph: graph, States: states})
	if err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, err
	}
	current, err := changeset.NewManifest(changeset.ManifestInput{
		ChangeSetID: record.ID,
		ProjectID:   record.ProjectID,
		Members:     members,
		Graph:       graph,
	})
	if err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, err
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, err
	}
	if currentDigest != record.PublicationDigest {
		return nil, changegraph.Graph{}, changeset.Result{}, errors.New("change set publication authority is stale")
	}
	var stored changeset.Manifest
	if err := json.Unmarshal(record.PublicationManifest, &stored); err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, fmt.Errorf("decode stored change set publication manifest: %w", err)
	}
	storedDigest, err := stored.Digest()
	if err != nil {
		return nil, changegraph.Graph{}, changeset.Result{}, fmt.Errorf("validate stored change set publication manifest: %w", err)
	}
	if storedDigest != record.PublicationDigest || stored.ChangeSetID != record.ID || stored.ProjectID != record.ProjectID {
		return nil, changegraph.Graph{}, changeset.Result{}, errors.New("change set publication manifest integrity check failed")
	}
	return members, graph, evaluation, nil
}

func (p *Publisher) loadChangeSetMembers(ctx context.Context, changeSetID string) ([]changeset.Member, error) {
	rows, err := p.db.QueryContext(ctx, `
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
		member := changeset.Member{ID: candidateID, CommitSHA: commitSHA, TreeHash: treeHash}
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
	if len(members) == 0 {
		return nil, errors.New("change set requires at least one candidate")
	}
	return members, nil
}

func (p *Publisher) loadProjectGraph(ctx context.Context, projectID string) (changegraph.Graph, map[string]changegraph.State, error) {
	rows, err := p.db.QueryContext(ctx, `
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
		graph.Nodes = append(graph.Nodes, changegraph.Node{ID: id, DependsOn: append([]string(nil), node.dependsOn...)})
	}
	if err := changegraph.Validate(graph); err != nil {
		return changegraph.Graph{}, nil, fmt.Errorf("invalid persisted change graph: %w", err)
	}
	return graph, states, nil
}

func (p *Publisher) loadPublicationMembers(ctx context.Context, changeSetID string) ([]publicationMember, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT cp.candidate_id, cp.ordinal, cp.status, cp.attempt_count, cp.merge_sha, cp.last_error,
		       c.pull_request_id, c.commit_sha, c.task_id, pr.number, r.owner, r.name
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
	members := make([]publicationMember, 0)
	for rows.Next() {
		var member publicationMember
		var mergeSHA, lastError sql.NullString
		if err := rows.Scan(
			&member.CandidateID, &member.Ordinal, &member.Status, &member.AttemptCount,
			&mergeSHA, &lastError, &member.PullRequestID, &member.CommitSHA,
			&member.TaskID, &member.PRNumber, &member.Owner, &member.RepoName,
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
	return members, rows.Err()
}

func (p *Publisher) renewLease(ctx context.Context, changeSetID, leaseToken string) error {
	now := time.Now().UTC()
	result, err := p.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_lease_until = $1, updated_at = $2
		WHERE id = $3 AND publication_lease_token = $4 AND publication_status = 'publishing'
	`, now.Add(p.leaseDuration), now, changeSetID, leaseToken)
	if err != nil {
		return fmt.Errorf("renew publication lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check publication lease renewal: %w", err)
	}
	if rows != 1 {
		return errors.New("change set publication lease was lost")
	}
	return nil
}

func (p *Publisher) reconcileMerged(ctx context.Context, changeSetID string, member *publicationMember, mergeSHA string) error {
	now := time.Now().UTC()
	if _, err := p.db.ExecContext(ctx, `
		UPDATE pull_requests SET state = 'merged', merged_at = COALESCE(merged_at, $1), updated_at = $1
		WHERE id = $2
	`, now, member.PullRequestID); err != nil {
		return fmt.Errorf("reconcile local pull request: %w", err)
	}
	if _, err := p.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'done', completed_at = COALESCE(completed_at, $1), updated_at = $1
		WHERE id = $2
	`, now, member.TaskID); err != nil {
		return fmt.Errorf("reconcile local task: %w", err)
	}
	return p.checkpointMerged(ctx, changeSetID, member.CandidateID, mergeSHA)
}

func (p *Publisher) checkpointMerged(ctx context.Context, changeSetID, candidateID, mergeSHA string) error {
	now := time.Now().UTC()
	if _, err := p.db.ExecContext(ctx, `
		UPDATE change_set_publications SET status = 'merged',
		    merge_sha = $1, last_error = NULL, completed_at = $2, updated_at = $2
		WHERE change_set_id = $3 AND candidate_id = $4
	`, mergeSHA, now, changeSetID, candidateID); err != nil {
		return fmt.Errorf("checkpoint merged publication member: %w", err)
	}
	return nil
}

func (p *Publisher) blockFailure(ctx context.Context, record *changeSetRecord, leaseToken string, actor changeauthority.Actor, candidateID string, err error) error {
	now := time.Now().UTC()
	if candidateID != "" {
		_, _ = p.db.ExecContext(ctx, `
			UPDATE change_set_publications
			SET status = 'blocked', last_error = $1, updated_at = $2
			WHERE change_set_id = $3 AND candidate_id = $4
		`, err.Error(), now, record.ID, candidateID)
	}
	_, _ = p.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = 'blocked', publication_lease_token = NULL,
		    publication_lease_until = NULL, updated_at = $1
		WHERE id = $2 AND publication_lease_token = $3
	`, now, record.ID, leaseToken)
	p.publishLifecycle(events.ChangeSetPublicationBlocked, record, actor, candidateID, err.Error())
	return &PublishError{Kind: ErrorBlocked, CandidateID: candidateID, Err: err}
}

func (p *Publisher) transientFailure(ctx context.Context, record *changeSetRecord, leaseToken, candidateID string, err error) error {
	now := time.Now().UTC()
	if candidateID != "" {
		_, _ = p.db.ExecContext(ctx, `
			UPDATE change_set_publications
			SET status = 'pending', last_error = $1, updated_at = $2
			WHERE change_set_id = $3 AND candidate_id = $4
		`, err.Error(), now, record.ID, candidateID)
	}
	_, _ = p.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = 'pending', publication_lease_token = NULL,
		    publication_lease_until = NULL, updated_at = $1
		WHERE id = $2 AND publication_lease_token = $3
	`, now, record.ID, leaseToken)
	return &PublishError{Kind: ErrorRetryable, CandidateID: candidateID, Err: err}
}

func (p *Publisher) markSetBlocked(ctx context.Context, changeSetID string) {
	_, _ = p.db.ExecContext(ctx, `
		UPDATE change_sets
		SET publication_status = 'blocked', publication_lease_token = NULL,
		    publication_lease_until = NULL, updated_at = $1
		WHERE id = $2
	`, time.Now().UTC(), changeSetID)
}

func (p *Publisher) publishLifecycle(subject string, record *changeSetRecord, actor changeauthority.Actor, candidateID, errText string) {
	if p.eventBus == nil {
		return
	}
	event := events.ChangeSetPublicationEvent{
		ChangeSetID:    record.ID,
		ProjectID:      record.ProjectID,
		ActorID:        actor.UserID,
		OrganizationID: actor.OrganizationID,
		Status:         lifecycleStatus(subject),
		CandidateID:    candidateID,
		Error:          errText,
	}
	data, err := json.Marshal(event)
	if err != nil {
		p.logger.Warn("failed to marshal change set publication lifecycle event", "subject", subject, "error", err)
		return
	}
	if err := p.eventBus.Publish(subject, data); err != nil {
		p.logger.Warn("failed to publish change set publication lifecycle event", "subject", subject, "error", err)
	}
}

func lifecycleStatus(subject string) string {
	switch subject {
	case events.ChangeSetPublicationStarted:
		return "publishing"
	case events.ChangeSetPublicationCompleted:
		return "completed"
	case events.ChangeSetPublicationBlocked:
		return "blocked"
	default:
		return ""
	}
}

func publicationResult(changeSetID, status string, members []publicationMember) *Result {
	result := &Result{ChangeSetID: changeSetID, PublicationStatus: status}
	result.Members = make([]MemberResult, 0, len(members))
	for _, member := range members {
		result.Members = append(result.Members, MemberResult{
			CandidateID:  member.CandidateID,
			Ordinal:      member.Ordinal,
			Status:       member.Status,
			AttemptCount: member.AttemptCount,
			MergeSHA:     member.MergeSHA,
			LastError:    member.LastError,
		})
	}
	return result
}

func formatBlockers(blockers []changeset.Blocker) string {
	if len(blockers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		parts = append(parts, blocker.CandidateID+":"+blocker.Reason)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
