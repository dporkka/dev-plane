package handlers

import (
	"context"
	"reflect"
	"testing"
)

func TestBuildAdmittedTaskCapsuleDerivesRequiredEvidenceFromProjectConfig(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		INSERT INTO tasks (
			id, project_id, repository_id, title, description, status, metadata, deleted_at
		) VALUES (
			'task-verify', 'project-1', 'repo-verify', 'Verify API change', '',
			'running',
			'{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}',
			NULL
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, updated_at
		) VALUES (
			'run-verify', 'task-verify', 'workspace-verify', 'implementer', 'gpt-5.6', 'openai',
			'admitting', '{}', CURRENT_TIMESTAMP
		);
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command
		) VALUES (
			'config-1', 'repo-verify', 'go test ./...', NULL, 'go build ./...', 'go build ./cmd/...'
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 4096})
	capsule, err := admission.BuildAdmittedTaskCapsule(
		context.Background(),
		"run-verify",
		"task-verify",
		nil,
	)
	if err != nil {
		t.Fatalf("BuildAdmittedTaskCapsule() error: %v", err)
	}

	want := []string{"tests", "typecheck", "build"}
	if !reflect.DeepEqual(capsule.RequiredEvidence, want) {
		t.Fatalf("required evidence = %#v, want %#v", capsule.RequiredEvidence, want)
	}
}

func TestBuildAdmittedTaskCapsuleExplicitEvidenceOverridesProjectConfig(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		INSERT INTO tasks (
			id, project_id, repository_id, title, description, status, metadata, deleted_at
		) VALUES (
			'task-override', 'project-1', 'repo-override', 'Override evidence', '',
			'running',
			'{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}',
			NULL
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, updated_at
		) VALUES (
			'run-override', 'task-override', 'workspace-override', 'implementer', 'gpt-5.6', 'openai',
			'admitting', '{}', CURRENT_TIMESTAMP
		);
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command
		) VALUES (
			'config-override', 'repo-override', 'go test ./...', 'go vet ./...', NULL, NULL
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 4096})
	capsule, err := admission.BuildAdmittedTaskCapsule(
		context.Background(),
		"run-override",
		"task-override",
		[]string{"security"},
	)
	if err != nil {
		t.Fatalf("BuildAdmittedTaskCapsule() error: %v", err)
	}
	if !reflect.DeepEqual(capsule.RequiredEvidence, []string{"security"}) {
		t.Fatalf("required evidence = %#v, want explicit override", capsule.RequiredEvidence)
	}
}
