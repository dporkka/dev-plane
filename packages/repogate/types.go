package repogate

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

const (
	StatusVerified   = "verified"
	StatusIntegrated = "integrated"
	StatusRejected   = "rejected"
	StatusConflict   = "conflict"
)

var (
	ErrReviewNotApprovable = errors.New("review is not approvable")
	ErrCandidateNotReady    = errors.New("verified candidate is not ready")
	ErrCandidateRejected    = errors.New("candidate verification failed")
)

type Record struct {
	RunID             string
	TaskID            string
	WorkspaceID       string
	CommitID          string
	ChangeID          string
	SourceBranch      string
	TargetBranch      string
	IntegrationBranch string
	IntegratedCommit  string
	Status            string
	InitialEvidence   map[string]string
	FinalEvidence     map[string]string
}

type workspaceContext struct {
	runID            string
	taskID           string
	workspaceID      string
	agentRole        string
	repositoryID     string
	sourceBranch     string
	baseBranch       string
	targetBranch     string
	worktreePath     string
	runtimeProvider  string
	runtimeSessionID string
	testCommand      string
	lintCommand      string
	typecheckCommand string
	buildCommand     string
	approvable       bool
}

type Gate struct {
	db        *sql.DB
	logger    *slog.Logger
	providers map[string]runtimes.Provider
	recorder  vcs.Recorder
}

func New(db *sql.DB, logger *slog.Logger) *Gate {
	if logger == nil {
		logger = slog.Default()
	}
	return &Gate{db: db, logger: logger, providers: make(map[string]runtimes.Provider), recorder: vcs.NopRecorder{}}
}

func (g *Gate) WithRuntimeProvider(name string, provider runtimes.Provider) *Gate {
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" && provider != nil {
		g.providers[name] = provider
	}
	return g
}

func (g *Gate) WithRecorder(recorder vcs.Recorder) *Gate {
	if recorder != nil {
		g.recorder = recorder
	}
	return g
}

type ReviewCandidateGate interface {
	PrepareReviewedCandidate(context.Context, string, string) (Record, error)
}

type ApprovedCandidateGate interface {
	IntegrateApproved(context.Context, string, string) (Record, error)
}
