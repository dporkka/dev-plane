package scheduler

import (
	"strings"
	"testing"
)

func TestBuildTaskCapsuleRejectsTaskThatIsNotAdmittedByScheduler(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 1,
		Capacity:    Capacity{CPU: 2, MemoryMB: 1024},
		Tasks: []Task{
			{
				ID:        "prepare",
				Owns:      []string{"packages/core"},
				Resources: Resources{CPU: 1, MemoryMB: 128},
			},
			{
				ID:        "implement",
				Owns:      []string{"packages/api"},
				DependsOn: []string{"prepare"},
				Resources: Resources{CPU: 1, MemoryMB: 128},
			},
		},
	}
	state := State{"prepare": StatusPending, "implement": StatusPending}

	_, err := BuildTaskCapsule(manifest, state, "implement", CapsuleOptions{
		Agent:       AgentIdentity{ID: "agent-1", Role: "implementer"},
		WorkspaceID: "workspace-1",
	})

	if err == nil || !strings.Contains(err.Error(), "not admitted") {
		t.Fatalf("expected scheduler admission error, got %v", err)
	}
}

func TestBuildTaskCapsuleCapturesIdentityLeasesDependenciesAndContext(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 2,
		Capacity:    Capacity{CPU: 2, MemoryMB: 1024},
		Tasks: []Task{
			{
				ID:        "prepare",
				Owns:      []string{"packages/core"},
				Resources: Resources{CPU: 1, MemoryMB: 128},
			},
			{
				ID:        "implement",
				Owns:      []string{"./packages/api/**", "docs/*"},
				DependsOn: []string{"prepare"},
				Resources: Resources{CPU: 1, MemoryMB: 128},
			},
		},
	}
	state := State{"prepare": StatusSuccess, "implement": StatusPending}

	capsule, err := BuildTaskCapsule(manifest, state, "implement", CapsuleOptions{
		Agent: AgentIdentity{
			ID:       "agent-1",
			Role:     "implementer",
			Provider: "openai",
			Model:    "gpt-5.6",
		},
		WorkspaceID: "workspace-1",
		Objective:   "Implement the API change",
		ContextRefs: []ContextRef{
			{Kind: "agentvault", URI: "agentvault://decision/42", Digest: "sha256:abc"},
			{Kind: "git", URI: "git://commit/abc123"},
		},
		RequiredEvidence: []string{"tests", "lint"},
	})
	if err != nil {
		t.Fatalf("build capsule: %v", err)
	}

	if capsule.Version != TaskCapsuleVersion {
		t.Fatalf("expected version %d, got %d", TaskCapsuleVersion, capsule.Version)
	}
	if capsule.TaskID != "implement" || capsule.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected task/workspace: %+v", capsule)
	}
	if capsule.Agent.ID != "agent-1" || capsule.Agent.Role != "implementer" {
		t.Fatalf("unexpected agent identity: %+v", capsule.Agent)
	}
	if len(capsule.DependsOn) != 1 || capsule.DependsOn[0] != "prepare" {
		t.Fatalf("unexpected dependencies: %#v", capsule.DependsOn)
	}
	if len(capsule.Leases) != 2 {
		t.Fatalf("expected 2 leases, got %#v", capsule.Leases)
	}
	if capsule.Leases[0] != (Lease{Path: "packages/api", Mode: LeaseModeExclusive}) {
		t.Fatalf("unexpected first lease: %+v", capsule.Leases[0])
	}
	if capsule.Leases[1] != (Lease{Path: "docs", Mode: LeaseModeExclusive}) {
		t.Fatalf("unexpected second lease: %+v", capsule.Leases[1])
	}
	if len(capsule.ContextRefs) != 2 || capsule.ContextRefs[0].URI != "agentvault://decision/42" {
		t.Fatalf("unexpected context refs: %#v", capsule.ContextRefs)
	}
	if len(capsule.RequiredEvidence) != 2 {
		t.Fatalf("unexpected required evidence: %#v", capsule.RequiredEvidence)
	}
}

func TestBuildTaskCapsuleRejectsInvalidIdentityAndContext(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 1,
		Capacity:    Capacity{CPU: 1, MemoryMB: 256},
		Tasks: []Task{{
			ID:        "task-1",
			Owns:      []string{"packages/api"},
			Resources: Resources{CPU: 1, MemoryMB: 128},
		}},
	}
	state := State{"task-1": StatusPending}

	_, err := BuildTaskCapsule(manifest, state, "task-1", CapsuleOptions{
		Agent:       AgentIdentity{Role: "implementer"},
		WorkspaceID: "workspace-1",
	})
	if err == nil || !strings.Contains(err.Error(), "agent id") {
		t.Fatalf("expected agent identity validation error, got %v", err)
	}

	_, err = BuildTaskCapsule(manifest, state, "task-1", CapsuleOptions{
		Agent:       AgentIdentity{ID: "agent-1", Role: "implementer"},
		WorkspaceID: "workspace-1",
		ContextRefs: []ContextRef{{Kind: "agentvault"}},
	})
	if err == nil || !strings.Contains(err.Error(), "context ref uri") {
		t.Fatalf("expected context ref validation error, got %v", err)
	}
}

func TestVerifyCompletionRequiresEveryNamedEvidenceCheckToPass(t *testing.T) {
	capsule := TaskCapsule{
		Version:          TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            AgentIdentity{ID: "agent-1", Role: "implementer"},
		RequiredEvidence: []string{"tests", "lint"},
		Evidence: []Evidence{
			{Name: "tests", Kind: "command", Status: EvidenceStatusPassed, Command: "go test ./..."},
		},
	}

	err := VerifyCompletion(capsule)
	if err == nil || !strings.Contains(err.Error(), "lint") {
		t.Fatalf("expected missing lint evidence, got %v", err)
	}

	capsule.Evidence = append(capsule.Evidence,
		Evidence{Name: "lint", Kind: "command", Status: EvidenceStatusPassed, Command: "golangci-lint run"},
	)
	if err := VerifyCompletion(capsule); err != nil {
		t.Fatalf("expected complete capsule to verify, got %v", err)
	}
}

func TestVerifyCompletionUsesLatestEvidenceAndRejectsFailure(t *testing.T) {
	capsule := TaskCapsule{
		Version:          TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            AgentIdentity{ID: "agent-1", Role: "implementer"},
		RequiredEvidence: []string{"tests"},
		Evidence: []Evidence{
			{Name: "tests", Kind: "command", Status: EvidenceStatusPassed, Command: "go test ./..."},
			{Name: "tests", Kind: "command", Status: EvidenceStatusFailed, Command: "go test ./..."},
		},
	}

	err := VerifyCompletion(capsule)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("expected latest failed evidence to block completion, got %v", err)
	}
}


func TestVerifyCompletionRejectsEvidenceFromStaleSubjectRevision(t *testing.T) {
	capsule := TaskCapsule{
		Version:          TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            AgentIdentity{ID: "agent-1", Role: "implementer"},
		SubjectRevision:  "git:revision-b",
		RequiredEvidence: []string{"tests"},
		Evidence: []Evidence{
			{
				Name:            "tests",
				Kind:            "command",
				Status:          EvidenceStatusPassed,
				SubjectRevision: "git:revision-a",
			},
		},
	}

	err := VerifyCompletion(capsule)
	if err == nil || !strings.Contains(err.Error(), "current subject revision") {
		t.Fatalf("expected stale subject revision error, got %v", err)
	}
}

func TestVerifyCompletionIgnoresDelayedEvidenceFromOlderRevision(t *testing.T) {
	capsule := TaskCapsule{
		Version:          TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            AgentIdentity{ID: "agent-1", Role: "implementer"},
		SubjectRevision:  "git:revision-b",
		RequiredEvidence: []string{"tests"},
		Evidence: []Evidence{
			{
				Name:            "tests",
				Kind:            "command",
				Status:          EvidenceStatusPassed,
				SubjectRevision: "git:revision-b",
			},
			{
				Name:            "tests",
				Kind:            "command",
				Status:          EvidenceStatusFailed,
				SubjectRevision: "git:revision-a",
			},
		},
	}

	if err := VerifyCompletion(capsule); err != nil {
		t.Fatalf("VerifyCompletion() error: %v", err)
	}
}

func TestVerifyCompletionRequiresRevisionOnEvidenceForRevisionBoundCapsule(t *testing.T) {
	capsule := TaskCapsule{
		Version:          TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            AgentIdentity{ID: "agent-1", Role: "implementer"},
		SubjectRevision:  "git:revision-b",
		RequiredEvidence: []string{"tests"},
		Evidence: []Evidence{
			{
				Name:   "tests",
				Kind:   "command",
				Status: EvidenceStatusPassed,
			},
		},
	}

	err := VerifyCompletion(capsule)
	if err == nil || !strings.Contains(err.Error(), "subject revision") {
		t.Fatalf("expected missing subject revision error, got %v", err)
	}
}

func TestBuildTaskCapsuleCapturesSubjectRevision(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 1,
		Capacity: Capacity{
			CPU:      1,
			MemoryMB: 1,
		},
		Tasks: []Task{
			{ID: "task-1", Owns: []string{"apps/api"}},
		},
	}
	state := State{"task-1": StatusPending}

	capsule, err := BuildTaskCapsule(manifest, state, "task-1", CapsuleOptions{
		Agent:           AgentIdentity{ID: "agent-1", Role: "implementer"},
		WorkspaceID:     "workspace-1",
		SubjectRevision: "git:abc123",
	})
	if err != nil {
		t.Fatalf("BuildTaskCapsule() error: %v", err)
	}
	if capsule.SubjectRevision != "git:abc123" {
		t.Fatalf("subject revision = %q, want git:abc123", capsule.SubjectRevision)
	}
}
