package evaluation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
)

type Outcome string

const (
	OutcomePending  Outcome = "pending"
	OutcomeAccepted Outcome = "accepted"
	OutcomeRejected Outcome = "rejected"
)

type Evaluation struct {
	ID                    string
	TaskID                string
	AgentRunID            string
	Attempt               int
	Outcome               Outcome
	HumanInterventions    int
	HumanAttentionSeconds int
	WallClockSeconds      int
	AgentComputeSeconds   int
	TotalTokens           int
	TotalCost             float64
	TestsPassed           int
	TestsFailed           int
	ReviewFindings        int
	HumanChangeLines      int
	RevertedWithin7d      bool
	ProductionRegression  bool
	Model                 string
	Provider              string
	PromptVersion         string
	SkillVersion          string
	Strategy              string
	Metadata              string
}

type Summary struct {
	UniqueTasks                     int
	AcceptedTasks                   int
	FirstPassAccepted               int
	Attempts                        int
	HumanInterventions              int
	HumanAttentionSeconds           int
	WallClockSeconds                int
	AgentComputeSeconds             int
	TotalTokens                     int
	TotalCost                       float64
	TestsPassed                     int
	TestsFailed                     int
	ReviewFindings                  int
	HumanChangeLines                int
	RevertedWithin7d                int
	ProductionRegressions           int
	PendingAttempts                 int
	RejectedAttempts                int
	AcceptedPerHumanAttentionMinute float64
}

type Ledger struct {
	db *sql.DB
}

func NewLedger(db *sql.DB) *Ledger {
	return &Ledger{db: db}
}

func (l *Ledger) Record(ctx context.Context, evaluation Evaluation) error {
	if l == nil || l.db == nil {
		return errors.New("evaluation ledger database is required")
	}
	if evaluation.Outcome == "" {
		evaluation.Outcome = OutcomePending
	}
	if err := validateEvaluation(evaluation); err != nil {
		return err
	}
	if strings.TrimSpace(evaluation.ID) == "" {
		evaluation.ID = uuid.NewString()
	}
	if strings.TrimSpace(evaluation.Metadata) == "" {
		evaluation.Metadata = "{}"
	}

	_, err := l.db.ExecContext(ctx, `
		INSERT INTO task_evaluations (
			id, task_id, agent_run_id, attempt, outcome,
			human_interventions, human_attention_seconds, wall_clock_seconds,
			agent_compute_seconds, total_tokens, total_cost, tests_passed,
			tests_failed, review_findings, human_change_lines,
			reverted_within_7d, production_regression, model, provider,
			prompt_version, skill_version, strategy, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
		)
	`,
		evaluation.ID,
		evaluation.TaskID,
		nullableString(evaluation.AgentRunID),
		evaluation.Attempt,
		evaluation.Outcome,
		evaluation.HumanInterventions,
		evaluation.HumanAttentionSeconds,
		evaluation.WallClockSeconds,
		evaluation.AgentComputeSeconds,
		evaluation.TotalTokens,
		evaluation.TotalCost,
		evaluation.TestsPassed,
		evaluation.TestsFailed,
		evaluation.ReviewFindings,
		evaluation.HumanChangeLines,
		evaluation.RevertedWithin7d,
		evaluation.ProductionRegression,
		nullableString(evaluation.Model),
		nullableString(evaluation.Provider),
		nullableString(evaluation.PromptVersion),
		nullableString(evaluation.SkillVersion),
		nullableString(evaluation.Strategy),
		evaluation.Metadata,
	)
	if err != nil {
		return fmt.Errorf("record task evaluation: %w", err)
	}
	return nil
}


// RecordNextAttempt atomically assigns the next task attempt and is idempotent
// for redelivered completion events from the same agent run.
func (l *Ledger) RecordNextAttempt(ctx context.Context, evaluation Evaluation) (int, error) {
	if l == nil || l.db == nil {
		return 0, errors.New("evaluation ledger database is required")
	}
	if strings.TrimSpace(evaluation.AgentRunID) == "" {
		return 0, errors.New("agent run ID is required for automatic attempt assignment")
	}
	if evaluation.Outcome == "" {
		evaluation.Outcome = OutcomePending
	}
	// validateEvaluation requires a positive attempt; the persisted attempt is
	// assigned by the INSERT subquery below.
	evaluation.Attempt = 1
	if err := validateEvaluation(evaluation); err != nil {
		return 0, err
	}
	if strings.TrimSpace(evaluation.ID) == "" {
		evaluation.ID = uuid.NewString()
	}
	if strings.TrimSpace(evaluation.Metadata) == "" {
		evaluation.Metadata = "{}"
	}

	var attempt int
	err := l.db.QueryRowContext(ctx, `
		INSERT INTO task_evaluations (
			id, task_id, agent_run_id, attempt, outcome,
			human_interventions, human_attention_seconds, wall_clock_seconds,
			agent_compute_seconds, total_tokens, total_cost, tests_passed,
			tests_failed, review_findings, human_change_lines,
			reverted_within_7d, production_regression, model, provider,
			prompt_version, skill_version, strategy, metadata
		) VALUES (
			$1, $2, $3,
			(SELECT COALESCE(MAX(attempt), 0) + 1 FROM task_evaluations WHERE task_id = $2),
			$4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22
		)
		ON CONFLICT(agent_run_id) DO NOTHING
		RETURNING attempt
	`,
		evaluation.ID,
		evaluation.TaskID,
		evaluation.AgentRunID,
		evaluation.Outcome,
		evaluation.HumanInterventions,
		evaluation.HumanAttentionSeconds,
		evaluation.WallClockSeconds,
		evaluation.AgentComputeSeconds,
		evaluation.TotalTokens,
		evaluation.TotalCost,
		evaluation.TestsPassed,
		evaluation.TestsFailed,
		evaluation.ReviewFindings,
		evaluation.HumanChangeLines,
		evaluation.RevertedWithin7d,
		evaluation.ProductionRegression,
		nullableString(evaluation.Model),
		nullableString(evaluation.Provider),
		nullableString(evaluation.PromptVersion),
		nullableString(evaluation.SkillVersion),
		nullableString(evaluation.Strategy),
		evaluation.Metadata,
	).Scan(&attempt)
	if err == nil {
		return attempt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("record next task evaluation attempt: %w", err)
	}

	if err := l.db.QueryRowContext(ctx, `
		SELECT attempt FROM task_evaluations WHERE agent_run_id = $1
	`, evaluation.AgentRunID).Scan(&attempt); err != nil {
		return 0, fmt.Errorf("load existing task evaluation attempt: %w", err)
	}
	return attempt, nil
}

func (l *Ledger) GetTask(ctx context.Context, taskID string) ([]Evaluation, error) {
	if l == nil || l.db == nil {
		return nil, errors.New("evaluation ledger database is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task ID is required")
	}

	rows, err := l.db.QueryContext(ctx, `
		SELECT
			id, task_id, agent_run_id, attempt, outcome,
			human_interventions, human_attention_seconds, wall_clock_seconds,
			agent_compute_seconds, total_tokens, total_cost, tests_passed,
			tests_failed, review_findings, human_change_lines,
			reverted_within_7d, production_regression, model, provider,
			prompt_version, skill_version, strategy, metadata
		FROM task_evaluations
		WHERE task_id = $1
		ORDER BY attempt ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task evaluations: %w", err)
	}
	defer rows.Close()

	var evaluations []Evaluation
	for rows.Next() {
		var evaluation Evaluation
		var agentRunID, model, provider, promptVersion, skillVersion, strategy sql.NullString

		if err := rows.Scan(
			&evaluation.ID,
			&evaluation.TaskID,
			&agentRunID,
			&evaluation.Attempt,
			&evaluation.Outcome,
			&evaluation.HumanInterventions,
			&evaluation.HumanAttentionSeconds,
			&evaluation.WallClockSeconds,
			&evaluation.AgentComputeSeconds,
			&evaluation.TotalTokens,
			&evaluation.TotalCost,
			&evaluation.TestsPassed,
			&evaluation.TestsFailed,
			&evaluation.ReviewFindings,
			&evaluation.HumanChangeLines,
			&evaluation.RevertedWithin7d,
			&evaluation.ProductionRegression,
			&model,
			&provider,
			&promptVersion,
			&skillVersion,
			&strategy,
			&evaluation.Metadata,
		); err != nil {
			return nil, fmt.Errorf("scan task evaluation: %w", err)
		}

		evaluation.AgentRunID = agentRunID.String
		evaluation.Model = model.String
		evaluation.Provider = provider.String
		evaluation.PromptVersion = promptVersion.String
		evaluation.SkillVersion = skillVersion.String
		evaluation.Strategy = strategy.String
		evaluations = append(evaluations, evaluation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task evaluations: %w", err)
	}

	return evaluations, nil
}

func Summarize(evaluations []Evaluation) Summary {
	var summary Summary
	seenTasks := make(map[string]struct{})
	acceptedTasks := make(map[string]struct{})
	firstPassAccepted := make(map[string]struct{})

	for _, evaluation := range evaluations {
		if evaluation.TaskID != "" {
			seenTasks[evaluation.TaskID] = struct{}{}
			if evaluation.Outcome == OutcomeAccepted {
				acceptedTasks[evaluation.TaskID] = struct{}{}
			}
			if evaluation.Outcome == OutcomeAccepted && evaluation.Attempt == 1 {
				firstPassAccepted[evaluation.TaskID] = struct{}{}
			}
		}

		summary.Attempts++
		summary.HumanInterventions += evaluation.HumanInterventions
		summary.HumanAttentionSeconds += evaluation.HumanAttentionSeconds
		summary.WallClockSeconds += evaluation.WallClockSeconds
		summary.AgentComputeSeconds += evaluation.AgentComputeSeconds
		summary.TotalTokens += evaluation.TotalTokens
		summary.TotalCost += evaluation.TotalCost
		summary.TestsPassed += evaluation.TestsPassed
		summary.TestsFailed += evaluation.TestsFailed
		summary.ReviewFindings += evaluation.ReviewFindings
		summary.HumanChangeLines += evaluation.HumanChangeLines
		if evaluation.RevertedWithin7d {
			summary.RevertedWithin7d++
		}
		if evaluation.ProductionRegression {
			summary.ProductionRegressions++
		}
		switch evaluation.Outcome {
		case OutcomePending:
			summary.PendingAttempts++
		case OutcomeRejected:
			summary.RejectedAttempts++
		}
	}

	summary.UniqueTasks = len(seenTasks)
	summary.AcceptedTasks = len(acceptedTasks)
	summary.FirstPassAccepted = len(firstPassAccepted)
	summary.TotalCost = math.Round(summary.TotalCost*1_000_000) / 1_000_000

	if summary.HumanAttentionSeconds > 0 {
		humanMinutes := float64(summary.HumanAttentionSeconds) / 60
		summary.AcceptedPerHumanAttentionMinute = float64(summary.AcceptedTasks) / humanMinutes
	}

	return summary
}

func SummarizeByStrategy(evaluations []Evaluation) map[string]Summary {
	grouped := make(map[string][]Evaluation)
	for _, evaluation := range evaluations {
		strategy := strings.TrimSpace(evaluation.Strategy)
		if strategy == "" {
			strategy = "unspecified"
		}
		grouped[strategy] = append(grouped[strategy], evaluation)
	}

	summaries := make(map[string]Summary, len(grouped))
	for strategy, strategyEvaluations := range grouped {
		summaries[strategy] = Summarize(strategyEvaluations)
	}
	return summaries
}

func validateEvaluation(evaluation Evaluation) error {
	if strings.TrimSpace(evaluation.TaskID) == "" {
		return errors.New("task ID is required")
	}
	if evaluation.Attempt < 1 {
		return errors.New("attempt must be at least 1")
	}
	switch evaluation.Outcome {
	case OutcomePending, OutcomeAccepted, OutcomeRejected:
	default:
		return fmt.Errorf("invalid outcome %q", evaluation.Outcome)
	}

	metrics := map[string]int{
		"human interventions":     evaluation.HumanInterventions,
		"human attention seconds": evaluation.HumanAttentionSeconds,
		"wall clock seconds":      evaluation.WallClockSeconds,
		"agent compute seconds":   evaluation.AgentComputeSeconds,
		"total tokens":            evaluation.TotalTokens,
		"tests passed":            evaluation.TestsPassed,
		"tests failed":            evaluation.TestsFailed,
		"review findings":         evaluation.ReviewFindings,
		"human change lines":      evaluation.HumanChangeLines,
	}
	for name, value := range metrics {
		if value < 0 {
			return fmt.Errorf("%s cannot be negative", name)
		}
	}
	if evaluation.TotalCost < 0 {
		return errors.New("total cost cannot be negative")
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
