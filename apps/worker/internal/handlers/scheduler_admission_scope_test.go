package handlers

import (
	"context"
	"testing"
)

// The worker must not use task IDs from other projects/repositories as
// scheduler prerequisites, even if those tasks have completed.
func TestSchedulerAdmissionDependencyScopeFailClosed(t *testing.T) {
	cases := []struct {
		name             string
		project          string
		repo             string
		createDependency bool
	}{
		{name: "same project and repository", project: "project-1", repo: "repo-1", createDependency: true},
		{name: "other project", project: "project-2", repo: "repo-1", createDependency: true},
		{name: "other repository", project: "project-1", repo: "repo-2", createDependency: true},
		{name: "nonexistent prerequisite", createDependency: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupSchedulerAdmissionDB(t)
			defer db.Close()
			if tc.createDependency {
				insertSchedulerTask(t, db, "other-task", "", "done", `{"scheduler":{"owns":["src/shared"]}}`)
				_, err := db.Exec("UPDATE tasks SET project_id = ?, repository_id = ? WHERE id = ?", tc.project, tc.repo, "other-task")
				if err != nil {
					t.Fatal(err)
				}
			}
			insertSchedulerTask(t, db, "candidate", "run-candidate", "approved", `{"scheduler":{"owns":["src/candidate"],"depends_on":["other-task"]}}`)

			admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 2048})
			decision, err := admission.AdmitRun(context.Background(), "run-candidate", "candidate")
			if err != nil {
				t.Fatal(err)
			}
			sameScope := tc.createDependency && tc.project == "project-1" && tc.repo == "repo-1"
			if decision.Allowed != sameScope {
				t.Fatalf("allowed = %v, reason %q; want %v", decision.Allowed, decision.Reason, sameScope)
			}
			if !sameScope && decision.Reason != "dependency-unavailable" {
				t.Fatalf("reason = %q, want indistinguishable dependency-unavailable", decision.Reason)
			}
			if !sameScope {
				var status string
				if err := db.QueryRow("SELECT status FROM agent_runs WHERE id = ?", "run-candidate").Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "queued" {
					t.Fatalf("denied run must remain queued, got %q", status)
				}
			}
		})
	}
}

func TestSchedulerAdmissionRejectsSelfDependencyBeforeClaim(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "candidate", "run-candidate", "approved", `{"scheduler":{"owns":["src/candidate"],"depends_on":["candidate"]}}`)
	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 2048})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "dependency-invalid" {
		t.Fatalf("decision = %+v", decision)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM agent_runs WHERE id = ?", "run-candidate").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("self-dependent run was claimed: %q", status)
	}
}

func TestSchedulerAdmissionMissingBackendFailsClosed(t *testing.T) {
	var admission *SchedulerAdmission
	decision, err := admission.AdmitRun(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "scheduler-unavailable" {
		t.Fatalf("nil scheduler granted admission: %+v", decision)
	}
	admission = NewSchedulerAdmission(nil, SchedulerCapacity{})
	decision, err = admission.AdmitRun(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "scheduler-unavailable" {
		t.Fatalf("scheduler without DB granted admission: %+v", decision)
	}
}
