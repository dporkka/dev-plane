package db

import (
	"path/filepath"
	"testing"
)

func TestRoutingDecisionTelemetryMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "routing-decisions.db")
	database, err := New("file:" + dbPath)
	if err != nil {
		t.Fatalf("New() sqlite error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations(sqlite) error: %v", err)
	}

	wantColumns := map[string]bool{
		"id":                          false,
		"agent_run_id":                false,
		"task_id":                     false,
		"step_number":                 false,
		"route":                       false,
		"route_source":                false,
		"policy_version":              false,
		"task_type":                   false,
		"difficulty":                  false,
		"agent_role":                  false,
		"risk_level":                  false,
		"model":                       false,
		"provider":                    false,
		"spend_authority":             false,
		"prompt_tokens":               false,
		"completion_tokens":           false,
		"total_tokens":                false,
		"estimated_cost":              false,
		"latency_ms":                  false,
		"provider_call_succeeded":     false,
		"outcome_status":              false,
		"verifier_passed":             false,
		"verifier_results":            false,
		"human_intervention_required": false,
		"error":                       false,
		"created_at":                  false,
		"outcome_recorded_at":         false,
	}

	rows, err := database.Query(`PRAGMA table_info(routing_decisions)`)
	if err != nil {
		t.Fatalf("table_info routing_decisions: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan routing_decisions column: %v", err)
		}
		if _, ok := wantColumns[name]; ok {
			wantColumns[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate routing_decisions columns: %v", err)
	}

	for name, found := range wantColumns {
		if !found {
			t.Errorf("routing_decisions missing column %q", name)
		}
	}
}
