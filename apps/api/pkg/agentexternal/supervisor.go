package agentexternal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
	"github.com/ai-dev-control-plane/api/pkg/agentapproval"
	"github.com/ai-dev-control-plane/events"
)

// RuntimeManager is the durable provider-neutral runtime subset required by the supervisor.
type RuntimeManager interface {
	CreateThread(ctx context.Context, providerName string, req agentruntime.CreateThreadRequest) (*agentruntime.Thread, error)
	ResumeThread(ctx context.Context, threadID string) (*agentruntime.Thread, error)
	RunTurn(ctx context.Context, req agentruntime.RunTurnRequest) (*agentruntime.RunStream, error)
}

// CompletionRequest identifies the exact provider turn that is asking to cross
// Dev Plane's verified-completion boundary.
type CompletionRequest struct {
	RunID       string
	TaskID      string
	WorkspaceID string
	ThreadID    string
	TurnID      string
}

// CompletionGate owns the authority to decide whether provider success is
// sufficient for Dev Plane completion. Implementations should bind verification
// evidence to the current workspace revision.
type CompletionGate interface {
	VerifyExternalRunCompletion(ctx context.Context, req CompletionRequest) error
}

// Publisher is the event-bus subset used for canonical run lifecycle events.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// SuspensionError indicates that the provider turn is still alive but the
// scheduler can release its admission claim because the durable run is paused.
type SuspensionError struct {
	RunID string
}

func (e *SuspensionError) Error() string {
	return fmt.Sprintf("external agent run %s durably suspended", e.RunID)
}

func (e *SuspensionError) RunSuspended() bool { return true }

type runContext struct {
	RunID              string
	TaskID             string
	WorkspaceID        string
	AgentRole          string
	Model              string
	TaskTitle          string
	TaskDescription    string
	Spec               string
	AcceptanceCriteria string
	MetadataRaw        string
	Provider           string
	ThreadID           string
}

// Supervisor owns active external-agent streams independently of a worker's
// scheduler admission lifetime.
type Supervisor struct {
	db        *sql.DB
	manager   RuntimeManager
	gate      CompletionGate
	publisher Publisher
	now       func() time.Time

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

// NewSupervisor creates an external-agent run supervisor.
func NewSupervisor(db *sql.DB, manager RuntimeManager, gate CompletionGate, publisher Publisher) *Supervisor {
	return &Supervisor{
		db:        db,
		manager:   manager,
		gate:      gate,
		publisher: publisher,
		now:       func() time.Time { return time.Now().UTC() },
		active:    make(map[string]context.CancelFunc),
	}
}

// ExecuteExternalRun launches or resumes one durable external-agent run.
// A durable approval pause returns SuspensionError while supervision continues.
func (s *Supervisor) ExecuteExternalRun(ctx context.Context, runID string) error {
	if s == nil || s.db == nil || s.manager == nil {
		return errors.New("external agent supervisor is not configured")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("agent run id is required")
	}

	run, err := s.loadRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Provider == "" {
		return errors.New("agent runtime execution provider is required")
	}

	thread, err := s.loadOrCreateThread(ctx, &run)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	if !s.register(runID, cancel) {
		cancel()
		return fmt.Errorf("external agent run %s is already supervised", runID)
	}

	stream, err := s.manager.RunTurn(runCtx, agentruntime.RunTurnRequest{
		ThreadID: thread.ID,
		Input:    agentruntime.TurnInput{Text: buildTaskPrompt(run)},
		Model:    run.Model,
	})
	if err != nil {
		s.unregister(runID)
		cancel()
		return fmt.Errorf("start external agent turn: %w", err)
	}

	if err := s.markStarted(ctx, run, thread.ID); err != nil {
		s.unregister(runID)
		cancel()
		return err
	}

	firstOutcome := make(chan error, 1)
	go s.supervise(runCtx, cancel, run, thread.ID, stream, firstOutcome)

	select {
	case err := <-firstOutcome:
		return err
	case <-ctx.Done():
		s.stop(runID)
		return ctx.Err()
	}
}

func (s *Supervisor) supervise(
	ctx context.Context,
	cancel context.CancelFunc,
	run runContext,
	threadID string,
	stream *agentruntime.RunStream,
	firstOutcome chan<- error,
) {
	defer cancel()
	defer s.unregister(run.RunID)

	signaled := false
	signal := func(err error) {
		if signaled {
			return
		}
		signaled = true
		firstOutcome <- err
	}

	if stream == nil {
		err := errors.New("external agent runtime returned nil stream")
		s.failRun(context.Background(), run, err)
		signal(err)
		return
	}

	eventsCh := stream.Events
	errorsCh := stream.Errors
	for eventsCh != nil || errorsCh != nil {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if !signaled {
				signal(err)
			}
			return
		case streamErr, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				continue
			}
			if streamErr == nil {
				continue
			}
			err := fmt.Errorf("external agent stream: %w", streamErr)
			s.failRun(context.Background(), run, err)
			signal(err)
			return
		case event, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}

			if event.Status == agentruntime.TurnStatusPausedApproval &&
				event.Item != nil &&
				event.Item.Type == agentruntime.ItemTypeApproval {
				signal(&SuspensionError{RunID: run.RunID})
				continue
			}

			if event.Type == agentruntime.EventTypeTurnFailed ||
				event.Status == agentruntime.TurnStatusFailed ||
				event.Status == agentruntime.TurnStatusInterrupted {
				reason := strings.TrimSpace(event.Error)
				if reason == "" {
					reason = "external agent turn failed"
				}
				err := errors.New(reason)
				s.failRun(context.Background(), run, err)
				signal(err)
				return
			}

			if event.Type == agentruntime.EventTypeTurnCompleted &&
				event.Status == agentruntime.TurnStatusCompleted {
				err := s.completeRun(context.Background(), run, threadID, event.TurnID)
				signal(err)
				return
			}
		}
	}

	err := errors.New("external agent stream ended before a terminal event")
	s.failRun(context.Background(), run, err)
	signal(err)
}

func (s *Supervisor) completeRun(ctx context.Context, run runContext, threadID, turnID string) error {
	if s.gate == nil {
		err := errors.New("external run completion gate is not configured")
		s.failRun(ctx, run, err)
		return err
	}
	req := CompletionRequest{
		RunID:       run.RunID,
		TaskID:      run.TaskID,
		WorkspaceID: run.WorkspaceID,
		ThreadID:    threadID,
		TurnID:      turnID,
	}
	if err := s.gate.VerifyExternalRunCompletion(ctx, req); err != nil {
		wrapped := fmt.Errorf("verify external run completion: %w", err)
		s.failRun(ctx, run, wrapped)
		return wrapped
	}

	now := s.now()
	summary := "external agent turn completed and passed Dev Plane verification"
	if _, err := s.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'completed', summary = $1, error_message = NULL,
		    completed_at = $2, updated_at = $2
		WHERE id = $3
	`, summary, now, run.RunID); err != nil {
		return fmt.Errorf("complete external agent run %s: %w", run.RunID, err)
	}
	return s.publishRunEvent(events.AgentRunCompleted, run, "completed", nil)
}

func (s *Supervisor) failRun(ctx context.Context, run runContext, failure error) {
	message := "external agent run failed"
	if failure != nil && strings.TrimSpace(failure.Error()) != "" {
		message = failure.Error()
	}
	now := s.now()
	_, _ = s.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'failed', error_message = $1, completed_at = $2, updated_at = $2
		WHERE id = $3
	`, message, now, run.RunID)
	_ = s.publishRunEvent(events.AgentRunFailed, run, "failed", map[string]any{"error": message})
}

func (s *Supervisor) markStarted(ctx context.Context, run runContext, threadID string) error {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'running', started_at = COALESCE(started_at, $1),
		    error_message = NULL, updated_at = $1
		WHERE id = $2
	`, now, run.RunID); err != nil {
		return fmt.Errorf("mark external agent run started: %w", err)
	}
	return s.publishRunEvent(events.AgentRunStarted, run, "running", map[string]any{"thread_id": threadID})
}

func (s *Supervisor) publishRunEvent(subject string, run runContext, status string, data map[string]any) error {
	if s.publisher == nil {
		return nil
	}
	var raw json.RawMessage
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = encoded
	}
	payload, err := json.Marshal(events.AgentRunEvent{
		RunID:     run.RunID,
		TaskID:    run.TaskID,
		AgentRole: run.AgentRole,
		Status:    status,
		Data:      raw,
	})
	if err != nil {
		return err
	}
	return s.publisher.Publish(subject, payload)
}

func (s *Supervisor) loadRun(ctx context.Context, runID string) (runContext, error) {
	var run runContext
	err := s.db.QueryRowContext(ctx, `
		SELECT ar.id, ar.task_id, COALESCE(ar.workspace_id, ''), ar.agent_role,
		       COALESCE(ar.model, ''), COALESCE(ar.metadata, '{}'),
		       t.title, COALESCE(t.description, ''), COALESCE(t.spec, ''),
		       COALESCE(t.acceptance_criteria, '')
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		WHERE ar.id = $1 AND t.deleted_at IS NULL
	`, runID).Scan(
		&run.RunID,
		&run.TaskID,
		&run.WorkspaceID,
		&run.AgentRole,
		&run.Model,
		&run.MetadataRaw,
		&run.TaskTitle,
		&run.TaskDescription,
		&run.Spec,
		&run.AcceptanceCriteria,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return run, fmt.Errorf("external agent run %s not found", runID)
		}
		return run, fmt.Errorf("load external agent run: %w", err)
	}
	if strings.TrimSpace(run.WorkspaceID) == "" {
		return run, fmt.Errorf("external agent run %s has no workspace", runID)
	}

	var metadata struct {
		Execution struct {
			Backend  string `json:"backend"`
			Provider string `json:"provider"`
			ThreadID string `json:"thread_id,omitempty"`
		} `json:"execution"`
	}
	if err := json.Unmarshal([]byte(run.MetadataRaw), &metadata); err != nil {
		return run, fmt.Errorf("decode external run metadata: %w", err)
	}
	if strings.TrimSpace(metadata.Execution.Backend) != "agent_runtime" {
		return run, fmt.Errorf("run %s is not configured for agent_runtime execution", runID)
	}
	run.Provider = strings.ToLower(strings.TrimSpace(metadata.Execution.Provider))
	run.ThreadID = strings.TrimSpace(metadata.Execution.ThreadID)
	return run, nil
}

func (s *Supervisor) loadOrCreateThread(ctx context.Context, run *runContext) (*agentruntime.Thread, error) {
	if run.ThreadID != "" {
		thread, err := s.manager.ResumeThread(ctx, run.ThreadID)
		if err != nil {
			return nil, fmt.Errorf("resume external agent thread %s: %w", run.ThreadID, err)
		}
		return thread, nil
	}

	ownership, err := json.Marshal(agentapproval.Ownership{TaskID: run.TaskID, AgentRunID: run.RunID})
	if err != nil {
		return nil, err
	}
	thread, err := s.manager.CreateThread(ctx, run.Provider, agentruntime.CreateThreadRequest{
		WorkspaceID:  run.WorkspaceID,
		Model:        run.Model,
		Instructions: "Operate as the Dev Plane " + run.AgentRole + " agent. Follow repository instructions and remain within granted capabilities.",
		Metadata:     ownership,
	})
	if err != nil {
		return nil, fmt.Errorf("create external agent thread: %w", err)
	}
	if thread == nil || strings.TrimSpace(thread.ID) == "" {
		return nil, errors.New("external agent runtime returned an empty thread")
	}
	run.ThreadID = thread.ID
	if err := s.persistThreadID(ctx, run); err != nil {
		return nil, err
	}
	return thread, nil
}

func (s *Supervisor) persistThreadID(ctx context.Context, run *runContext) error {
	var metadata map[string]any
	if err := json.Unmarshal([]byte(run.MetadataRaw), &metadata); err != nil {
		return fmt.Errorf("decode run metadata for thread persistence: %w", err)
	}
	execution, ok := metadata["execution"].(map[string]any)
	if !ok || execution == nil {
		execution = make(map[string]any)
		metadata["execution"] = execution
	}
	execution["thread_id"] = run.ThreadID
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode run metadata with thread id: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE agent_runs SET metadata = $1, updated_at = $2 WHERE id = $3
	`, string(encoded), s.now(), run.RunID); err != nil {
		return fmt.Errorf("persist external agent thread id: %w", err)
	}
	run.MetadataRaw = string(encoded)
	return nil
}

func buildTaskPrompt(run runContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", run.TaskTitle)
	if strings.TrimSpace(run.TaskDescription) != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", run.TaskDescription)
	}
	if strings.TrimSpace(run.Spec) != "" && strings.TrimSpace(run.Spec) != "{}" {
		fmt.Fprintf(&b, "\nSpecification:\n%s\n", run.Spec)
	}
	if strings.TrimSpace(run.AcceptanceCriteria) != "" && strings.TrimSpace(run.AcceptanceCriteria) != "[]" {
		fmt.Fprintf(&b, "\nAcceptance criteria:\n%s\n", run.AcceptanceCriteria)
	}
	b.WriteString("\nImplement the task in the assigned workspace. Do not declare completion until the requested changes are ready for Dev Plane verification.")
	return b.String()
}

func (s *Supervisor) register(runID string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[runID]; exists {
		return false
	}
	s.active[runID] = cancel
	return true
}

func (s *Supervisor) unregister(runID string) {
	s.mu.Lock()
	delete(s.active, runID)
	s.mu.Unlock()
}

func (s *Supervisor) stop(runID string) {
	s.mu.Lock()
	cancel := s.active[runID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

var _ interface {
	ExecuteExternalRun(context.Context, string) error
} = (*Supervisor)(nil)
