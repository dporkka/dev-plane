package evaluation

import (
	"context"
	"database/sql"
	"math"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)\n\tt.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE task_evaluations (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			attempt INTEGER NOT NULL,
			accepted BOOLEAN NOT NULL DEFAULT false,
			first_pass BOOLEAN NOT NULL DEFAULT false,
			human_interventions INTEGER NOT NULL DEFAULT 0,
			human_attention_seconds INTEGER NOT NULL DEFAULT 0,
			wall_clock_seconds INTEGER NOT NULL DEFAULT 0,
			agent_compute_seconds INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			total_cost REAL NOT NULL DEFAULT 0,
			tests_passed INTEGER NOT NULL DEFAULT 0,
			tests_failed INTEGER NOT NULL DEFAULT 0,
			review_findings INTEGER NOT NULL DEFAULT 0,
			human_change_lines INTEGER NOT NULL DEFAULT 0,
			reverted_within_7d BOOLEAN NOT NULL DEFAULT false,
			production_regression BOOLEAN NOT NULL DEFAULT false,
			model TEXT,
			provider TEXT,
			prompt_version TEXT,
			skill_version TEXT,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(task_id, attempt)
		)
	`)
	if err != nil {
		t.Fatalf("create task_evaluations: %v", err)
	}

	return db
}

func TestLedgerRecordPersistsTaskOutcome(t *testing.T) {
	db := openTestDB(t)
	ledger := NewLedger(db)

	err := ledger.Record(context.Background(), Evaluation{
		ID:                    "eval-1",
		TaskID:                "task-1",
		AgentRunID:            "run-1",
		Attempt:               1,
		Accepted:              true,
		FirstPass:             true,
		HumanInterventions:    1,
		HumanAttentionSeconds: 120,
		WallClockSeconds:      600,
		AgentComputeSeconds:   480,
		TotalTokens:           12_000,
		TotalCost:             0.42,
		TestsPassed:           37,
		Model:                 "gpt-5",
		Provider:              "openai",
		PromptVersion:         "planner-v2",
		SkillVersion:          "go-tdd-v1",
	})
	if err != nil {
		t.Fatalf("Record() error: %v", err)
	}

	got, err := ledger.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("GetTask() len = %d, want 1", len(got))
	}

	eval := got[0]
	if !eval.Accepted || !eval.FirstPass {
		t.Fatalf("persisted outcome = accepted:%v first_pass:%v, want true/true", eval.Accepted, eval.FirstPass)
	}
	if eval.HumanAttentionSeconds != 120 || eval.TotalTokens != 12_000 {
		t.Fatalf("persisted metrics = attention:%d tokens:%d", eval.HumanAttentionSeconds, eval.TotalTokens)
	}
	if eval.TotalCost != 0.42 {
		t.Fatalf("persisted cost = %.2f, want 0.42", eval.TotalCost)
	}
}

func TestLedgerRecordRejectsInvalidMetrics(t *testing.T) {
	db := openTestDB(t)
	ledger := NewLedger(db)

	err := ledger.Record(context.Background(), Evaluation{
		ID:                    "eval-invalid",
		TaskID:                "task-1",
		Attempt:               0,
		HumanAttentionSeconds: -1,
	})
	if err == nil {
		t.Fatal("Record() error = nil, want validation error")
	}
}

func TestSummarizeMeasuresAcceptedWorkPerHumanAttentionMinute(t *testing.T) {
	summary := Summarize([]Evaluation{
		{
			TaskID:                "task-1",
			Attempt:               1,
			Accepted:              true,
			FirstPass:             true,
			HumanAttentionSeconds: 120,
			TotalCost:             0.40,
			TotalTokens:           10_000,
		},
		{
			TaskID:                "task-2",
			Attempt:               1,
			Accepted:              false,
			HumanAttentionSeconds: 60,
			TotalCost:             0.20,
			TotalTokens:           5_000,
		},
		{
			TaskID:                "task-2",
			Attempt:               2,
			Accepted:              true,
			HumanAttentionSeconds: 60,
			TotalCost:             0.25,
			TotalTokens:           6_000,
			ProductionRegression:  true,
		},
	})

	if summary.UniqueTasks != 2 {
		t.Fatalf("UniqueTasks = %d, want 2", summary.UniqueTasks)
	}
	if summary.AcceptedTasks != 2 {
		t.Fatalf("AcceptedTasks = %d, want 2", summary.AcceptedTasks)
	}
	if summary.FirstPassAccepted != 1 {
		t.Fatalf("FirstPassAccepted = %d, want 1", summary.FirstPassAccepted)
	}
	if summary.Attempts != 3 {
		t.Fatalf("Attempts = %d, want 3", summary.Attempts)
	}

	wantPerMinute := 0.5 // 2 accepted tasks / 4 human-attention minutes
	if math.Abs(summary.AcceptedPerHumanAttentionMinute-wantPerMinute) > 0.000001 {
		t.Fatalf("AcceptedPerHumanAttentionMinute = %.6f, want %.6f", summary.AcceptedPerHumanAttentionMinute, wantPerMinute)
	}
	if summary.TotalCost != 0.85 {
		t.Fatalf("TotalCost = %.2f, want 0.85", summary.TotalCost)
	}
	if summary.TotalTokens != 21_000 {
		t.Fatalf("TotalTokens = %d, want 21000", summary.TotalTokens)
	}
	if summary.ProductionRegressions != 1 {
		t.Fatalf("ProductionRegressions = %d, want 1", summary.ProductionRegressions)
	}
}

func TestSummarizeZeroAttentionDoesNotProduceInfinity(t *testing.T) {
	summary := Summarize([]Evaluation{{
		TaskID:   "task-1",
		Attempt:  1,
		Accepted: true,
	}})

	if summary.AcceptedPerHumanAttentionMinute != 0 {
		t.Fatalf("AcceptedPerHumanAttentionMinute = %f, want 0 when attention is unmeasured", summary.AcceptedPerHumanAttentionMinute)
	}
}
