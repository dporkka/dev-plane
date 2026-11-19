package scheduler

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func TestNextPreferUnblockingPrioritizesSharedDependencyWithoutChangingDefault(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 1,
		Capacity:    Capacity{CPU: 1, MemoryMB: 512},
		Tasks: []Task{
			{ID: "unrelated", Owns: []string{"docs"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "foundation", Owns: []string{"foundation"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "api", Owns: []string{"api"}, DependsOn: []string{"foundation"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "web", Owns: []string{"web"}, DependsOn: []string{"foundation"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
		},
	}
	original, err := Next(manifest, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.Ready, []string{"unrelated"}) {
		t.Fatalf("legacy selection = %v, want unrelated", original.Ready)
	}

	manifest.PrioritizeUnblocking = true
	preferred, err := Next(manifest, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preferred.Ready, []string{"foundation"}) {
		t.Fatalf("dependency-aware selection = %v, want foundation", preferred.Ready)
	}
}

func TestNextPreferUnblockingIgnoresTerminalDependentsAndPreservesStableTies(t *testing.T) {
	manifest := Manifest{
		MaxParallel:          1,
		Capacity:             Capacity{CPU: 1, MemoryMB: 512},
		PrioritizeUnblocking: true,
		Tasks: []Task{
			{ID: "clean", Owns: []string{"clean"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "obsolete", Owns: []string{"obsolete"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "cancelled", Owns: []string{"cancelled"}, DependsOn: []string{"obsolete"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
		},
	}
	decision, err := Next(manifest, State{"cancelled": StatusSkipped})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decision.Ready, []string{"clean"}) {
		t.Fatalf("selection = %v, want clean (obsolete's only dependent is terminal)", decision.Ready)
	}
}

func TestNextWorkCanEnableDependencyUnblockingPriority(t *testing.T) {
	items := []repoprotocol.WorkItem{
		workItem("unrelated", []string{"docs"}, 1),
		workItem("foundation", []string{"shared"}, 1),
		workItem("dependent", []string{"api"}, 1),
	}
	items[2].DependsOn = []string{"foundation"}
	decision, err := NextWork(items, WorkScheduleOptions{
		MaxParallelCost:      1,
		Now:                  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		PrioritizeUnblocking: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decision.Ready, []string{"foundation"}) {
		t.Fatalf("selection = %v, want foundation", decision.Ready)
	}
}

func TestNextPropagatesFailureToAllTransitiveDependents(t *testing.T) {
	manifest := Manifest{
		MaxParallel: 1,
		Capacity:    Capacity{CPU: 1, MemoryMB: 512},
		Tasks: []Task{
			{ID: "failed", Owns: []string{"a"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "direct", Owns: []string{"b"}, DependsOn: []string{"failed"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "transitive", Owns: []string{"c"}, DependsOn: []string{"direct"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
		},
	}
	result, err := Next(manifest, State{"failed": StatusFailed})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"direct":     "dependency-failed",
		"transitive": "dependency-failed",
	}
	if !reflect.DeepEqual(result.Skipped, expected) {
		t.Fatalf("skipped = %v, want %v", result.Skipped, expected)
	}
}

func TestUnblockingPriorityDoesNotRewardIrrecoverablyBlockedChildren(t *testing.T) {
	manifest := Manifest{
		MaxParallel:          1,
		Capacity:             Capacity{CPU: 1, MemoryMB: 512},
		PrioritizeUnblocking: true,
		Tasks: []Task{
			{ID: "unrelated", Owns: []string{"docs"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "candidate", Owns: []string{"candidate"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "failed", Owns: []string{"failed"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "doomed", Owns: []string{"doomed"}, DependsOn: []string{"failed"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
			{ID: "child", Owns: []string{"child"}, DependsOn: []string{"doomed", "candidate"}, Resources: Resources{CPU: 1, MemoryMB: 128}},
		},
	}
	result, err := Next(manifest, State{"failed": StatusFailed})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Ready, []string{"unrelated"}) {
		t.Fatalf("selection = %v, want unrelated because child's other prerequisite cannot succeed", result.Ready)
	}
}

func BenchmarkNextDeepDependencyChain(b *testing.B) {
	tasks := make([]Task, 1000)
	for i := range tasks {
		tasks[i] = Task{
			ID:        fmt.Sprintf("task-%04d", i),
			Owns:      []string{fmt.Sprintf("path-%04d", i)},
			Resources: Resources{CPU: 1, MemoryMB: 1},
		}
		if i > 0 {
			tasks[i].DependsOn = []string{tasks[i-1].ID}
		}
	}
	manifest := Manifest{
		MaxParallel:          1,
		Capacity:             Capacity{CPU: 1, MemoryMB: 1},
		Tasks:                tasks,
		PrioritizeUnblocking: true,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decision, err := Next(manifest, State{})
		if err != nil || len(decision.Ready) != 1 || decision.Ready[0] != tasks[0].ID {
			b.Fatalf("unexpected decision %v error %v", decision, err)
		}
	}
}
