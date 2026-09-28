package scheduler

import (
	"reflect"
	"strings"
	"testing"
)

func TestPlanWavesSerializesOwnershipConflictsAndOverlapsDisjointTasks(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 3,
		Capacity: Capacity{CPU: 4, MemoryMB: 4096},
		Tasks: []Task{
			{ID: "A", Owns: []string{"apps/api"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "B", Owns: []string{"apps/api/routes"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "C", Owns: []string{"apps/web"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
		},
	}

	waves, err := PlanWaves(manifest)
	if err != nil {
		t.Fatalf("PlanWaves() error = %v", err)
	}
	want := [][]string{{"A", "C"}, {"B"}}
	if !reflect.DeepEqual(waves, want) {
		t.Fatalf("PlanWaves() = %#v, want %#v", waves, want)
	}
}

func TestPlanWavesRespectsCapacity(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 4,
		Capacity: Capacity{CPU: 2, MemoryMB: 1024},
		Tasks: []Task{
			{ID: "A", Owns: []string{"a"}, Resources: Resources{CPU: 2, MemoryMB: 512}},
			{ID: "B", Owns: []string{"b"}, Resources: Resources{CPU: 2, MemoryMB: 512}},
			{ID: "C", Owns: []string{"c"}, Resources: Resources{CPU: 2, MemoryMB: 512}},
		},
	}

	waves, err := PlanWaves(manifest)
	if err != nil {
		t.Fatalf("PlanWaves() error = %v", err)
	}
	want := [][]string{{"A"}, {"B"}, {"C"}}
	if !reflect.DeepEqual(waves, want) {
		t.Fatalf("PlanWaves() = %#v, want %#v", waves, want)
	}
}

func TestPlanWavesHonorsDependencies(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 3,
		Capacity: Capacity{CPU: 3, MemoryMB: 3072},
		Tasks: []Task{
			{ID: "A", Owns: []string{"a"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "B", Owns: []string{"b"}, DependsOn: []string{"A"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "C", Owns: []string{"c"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
		},
	}

	waves, err := PlanWaves(manifest)
	if err != nil {
		t.Fatalf("PlanWaves() error = %v", err)
	}
	want := [][]string{{"A", "C"}, {"B"}}
	if !reflect.DeepEqual(waves, want) {
		t.Fatalf("PlanWaves() = %#v, want %#v", waves, want)
	}
}

func TestValidateRejectsDependencyCycle(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 2,
		Capacity: Capacity{CPU: 2, MemoryMB: 1024},
		Tasks: []Task{
			{ID: "A", Owns: []string{"a"}, DependsOn: []string{"B"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "B", Owns: []string{"b"}, DependsOn: []string{"A"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
		},
	}
	err := Validate(manifest)
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("Validate() error = %v, want dependency cycle", err)
	}
}

func TestValidateRejectsTaskThatCannotFitCapacity(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 2,
		Capacity: Capacity{CPU: 2, MemoryMB: 1024},
		Tasks: []Task{
			{ID: "A", Owns: []string{"a"}, Resources: Resources{CPU: 3, MemoryMB: 256}},
		},
	}
	err := Validate(manifest)
	if err == nil || !strings.Contains(err.Error(), "exceeds scheduler capacity") {
		t.Fatalf("Validate() error = %v, want capacity error", err)
	}
}

func TestOwnershipConflictUsesConservativePathPrefixes(t *testing.T) {
	cases := []struct {
		left, right []string
		want        bool
	}{
		{[]string{"apps/api"}, []string{"apps/api/routes"}, true},
		{[]string{"apps/api/routes"}, []string{"apps/api"}, true},
		{[]string{"apps/api"}, []string{"apps/web"}, false},
		{[]string{"packages/shared/"}, []string{"packages/shared/contracts/**"}, true},
	}
	for _, tc := range cases {
		if got := OwnershipConflict(tc.left, tc.right); got != tc.want {
			t.Fatalf("OwnershipConflict(%v, %v) = %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestReadyTasksSkipsFailedDependentsAndKeepsIndependentWork(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 3,
		Capacity: Capacity{CPU: 3, MemoryMB: 3072},
		Tasks: []Task{
			{ID: "A", Owns: []string{"a"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "B", Owns: []string{"b"}, DependsOn: []string{"A"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
			{ID: "C", Owns: []string{"c"}, Resources: Resources{CPU: 1, MemoryMB: 256}},
		},
	}
	state := State{
		"A": StatusFailed,
		"B": StatusPending,
		"C": StatusPending,
	}

	decision, err := Next(manifest, state)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !reflect.DeepEqual(decision.Ready, []string{"C"}) {
		t.Fatalf("Next().Ready = %#v, want [C]", decision.Ready)
	}
	if !reflect.DeepEqual(decision.Skipped, map[string]string{"B": "dependency-failed"}) {
		t.Fatalf("Next().Skipped = %#v", decision.Skipped)
	}
}

func TestNextAccountsForAlreadyRunningResourceAndOwnershipClaims(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 3,
		Capacity: Capacity{CPU: 4, MemoryMB: 4096},
		Tasks: []Task{
			{ID: "A", Owns: []string{"apps/api"}, Resources: Resources{CPU: 2, MemoryMB: 1024}},
			{ID: "B", Owns: []string{"apps/api/routes"}, Resources: Resources{CPU: 1, MemoryMB: 512}},
			{ID: "C", Owns: []string{"apps/web"}, Resources: Resources{CPU: 2, MemoryMB: 1024}},
			{ID: "D", Owns: []string{"packages/shared"}, Resources: Resources{CPU: 1, MemoryMB: 512}},
		},
	}
	state := State{
		"A": StatusRunning,
		"B": StatusPending,
		"C": StatusPending,
		"D": StatusPending,
	}

	decision, err := Next(manifest, state)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	// B conflicts with running A. C would exceed CPU with A. D can launch.
	if !reflect.DeepEqual(decision.Ready, []string{"D"}) {
		t.Fatalf("Next().Ready = %#v, want [D]", decision.Ready)
	}
}
