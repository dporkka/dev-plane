package scheduler

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

type Resources struct {
	CPU      float64 `json:"cpu"`
	MemoryMB int     `json:"memory_mb"`
}

type Capacity struct {
	CPU      float64 `json:"cpu"`
	MemoryMB int     `json:"memory_mb"`
}

type Task struct {
	ID        string    `json:"id"`
	Owns      []string  `json:"owns"`
	DependsOn []string  `json:"depends_on,omitempty"`
	Resources Resources `json:"resources"`
}

type Manifest struct {
	MaxParallel          int      `json:"max_parallel"`
	Capacity             Capacity `json:"capacity"`
	Tasks                []Task   `json:"tasks"`
	PrioritizeUnblocking bool     `json:"prioritize_unblocking,omitempty"`
}

type State map[string]Status

type Decision struct {
	Ready   []string          `json:"ready"`
	Skipped map[string]string `json:"skipped,omitempty"`
}

func Validate(manifest Manifest) error {
	if manifest.MaxParallel <= 0 {
		return errors.New("max_parallel must be positive")
	}
	if manifest.Capacity.CPU <= 0 {
		return errors.New("capacity cpu must be positive")
	}
	if manifest.Capacity.MemoryMB <= 0 {
		return errors.New("capacity memory_mb must be positive")
	}
	if len(manifest.Tasks) == 0 {
		return errors.New("scheduler requires at least one task")
	}

	tasks := make(map[string]Task, len(manifest.Tasks))
	for i := range manifest.Tasks {
		task := manifest.Tasks[i]
		task.ID = strings.TrimSpace(task.ID)
		if task.ID == "" {
			return fmt.Errorf("task at index %d requires an id", i)
		}
		if _, exists := tasks[task.ID]; exists {
			return fmt.Errorf("duplicate task id %q", task.ID)
		}
		if task.Resources.CPU <= 0 || task.Resources.MemoryMB <= 0 {
			return fmt.Errorf("task %s resources must be positive", task.ID)
		}
		if task.Resources.CPU > manifest.Capacity.CPU || task.Resources.MemoryMB > manifest.Capacity.MemoryMB {
			return fmt.Errorf("task %s exceeds scheduler capacity", task.ID)
		}
		if len(task.Owns) == 0 {
			return fmt.Errorf("task %s requires at least one ownership path", task.ID)
		}
		for _, ownership := range task.Owns {
			if _, err := normalizeOwnership(ownership); err != nil {
				return fmt.Errorf("task %s ownership: %w", task.ID, err)
			}
		}
		tasks[task.ID] = task
	}

	for _, task := range manifest.Tasks {
		for _, dependency := range task.DependsOn {
			if dependency == task.ID {
				return fmt.Errorf("dependency cycle detected at %s", task.ID)
			}
			if _, exists := tasks[dependency]; !exists {
				return fmt.Errorf("task %s depends on unknown task %s", task.ID, dependency)
			}
		}
	}

	visiting := make(map[string]bool, len(tasks))
	visited := make(map[string]bool, len(tasks))
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("dependency cycle detected at %s", id)
		}
		visiting[id] = true
		for _, dependency := range tasks[id].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, task := range manifest.Tasks {
		if err := visit(task.ID); err != nil {
			return err
		}
	}
	return nil
}

func OwnershipConflict(left, right []string) bool {
	for _, rawLeft := range left {
		a, err := normalizeOwnership(rawLeft)
		if err != nil {
			return true
		}
		for _, rawRight := range right {
			b, err := normalizeOwnership(rawRight)
			if err != nil {
				return true
			}
			if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
				return true
			}
		}
	}
	return false
}

func Next(manifest Manifest, state State) (Decision, error) {
	if err := Validate(manifest); err != nil {
		return Decision{}, err
	}

	tasks := make(map[string]Task, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		tasks[task.ID] = task
	}

	decision := Decision{Skipped: map[string]string{}}
	running := make([]Task, 0)
	used := Resources{}
	runningCount := 0
	for _, task := range manifest.Tasks {
		if state[task.ID] == StatusRunning {
			running = append(running, task)
			used.CPU += task.Resources.CPU
			used.MemoryMB += task.Resources.MemoryMB
			runningCount++
		}
	}

	// Propagate a failed prerequisite through the entire pending dependency
	// chain in this decision. Otherwise a grandchild appears merely blocked
	// and an integration wave can report a false deadlock.
	for changed := true; changed; {
		changed = false
		for _, task := range manifest.Tasks {
			if statusOf(state, task.ID) != StatusPending {
				continue
			}
			if _, skipped := decision.Skipped[task.ID]; skipped {
				continue
			}
			failed := dependencyFailed(task, state)
			for _, dependency := range task.DependsOn {
				if _, skipped := decision.Skipped[dependency]; skipped {
					failed = true
				}
			}
			if failed {
				decision.Skipped[task.ID] = "dependency-failed"
				changed = true
			}
		}
	}

	selected := append([]Task(nil), running...)
	pending := append([]Task(nil), manifest.Tasks...)
	if manifest.PrioritizeUnblocking {
		reverse := make(map[string][]string, len(tasks))
		for _, task := range manifest.Tasks {
			for _, dependency := range task.DependsOn {
				reverse[dependency] = append(reverse[dependency], task.ID)
			}
		}
		scores := make(map[string]int, len(tasks))
		for _, task := range pending {
			// Only currently runnable candidates compete for a slot. Scoring
			// blocked or terminal tasks does quadratic wasted graph walks on
			// long dependency chains while leaving the decision unchanged.
			if statusOf(state, task.ID) != StatusPending || !dependenciesSucceeded(task, state) {
				continue
			}
			if _, skipped := decision.Skipped[task.ID]; skipped {
				continue
			}
			scores[task.ID] = pendingDependents(task.ID, reverse, tasks, state, decision.Skipped)
		}
		sort.SliceStable(pending, func(i, j int) bool {
			return scores[pending[i].ID] > scores[pending[j].ID]
		})
	}
	for _, task := range pending {
		if statusOf(state, task.ID) != StatusPending {
			continue
		}
		if _, skipped := decision.Skipped[task.ID]; skipped {
			continue
		}
		if !dependenciesSucceeded(task, state) {
			continue
		}
		if runningCount+len(decision.Ready) >= manifest.MaxParallel {
			break
		}
		if used.CPU+task.Resources.CPU > manifest.Capacity.CPU ||
			used.MemoryMB+task.Resources.MemoryMB > manifest.Capacity.MemoryMB {
			continue
		}
		if conflictsAny(task, selected) {
			continue
		}
		decision.Ready = append(decision.Ready, task.ID)
		selected = append(selected, task)
		used.CPU += task.Resources.CPU
		used.MemoryMB += task.Resources.MemoryMB
	}

	if len(decision.Skipped) == 0 {
		decision.Skipped = nil
	}
	return decision, nil
}

func PlanWaves(manifest Manifest) ([][]string, error) {
	if err := Validate(manifest); err != nil {
		return nil, err
	}
	state := make(State, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		state[task.ID] = StatusPending
	}

	waves := make([][]string, 0)
	remaining := len(manifest.Tasks)
	for remaining > 0 {
		decision, err := Next(manifest, state)
		if err != nil {
			return nil, err
		}
		for id := range decision.Skipped {
			state[id] = StatusSkipped
			remaining--
		}
		if len(decision.Ready) == 0 {
			if remaining == 0 {
				break
			}
			return nil, errors.New("scheduler deadlocked despite a valid dependency graph")
		}
		wave := append([]string(nil), decision.Ready...)
		waves = append(waves, wave)
		for _, id := range wave {
			state[id] = StatusSuccess
			remaining--
		}
	}
	return waves, nil
}

// pendingDependents counts distinct unfinished tasks reachable through the
// dependency graph. Terminal and irrecoverably blocked nodes never create
// artificial urgency. This is an opt-in ordering hint, not permission to
// bypass resource, dependency, or ownership checks.
func pendingDependents(id string, reverse map[string][]string, tasks map[string]Task, state State, skipped map[string]string) int {
	seen := map[string]bool{id: true}
	queue := []string{id}
	count := 0
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, childID := range reverse[parent] {
			if seen[childID] {
				continue
			}
			seen[childID] = true
			if _, failed := skipped[childID]; failed {
				continue
			}
			child := tasks[childID]
			if statusOf(state, childID) != StatusPending || dependencyFailed(child, state) {
				continue
			}
			count++
			queue = append(queue, childID)
		}
	}
	return count
}

func normalizeOwnership(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimSuffix(value, "/**")
	value = strings.TrimSuffix(value, "/*")
	value = strings.TrimSuffix(value, "/")
	if value == "" || strings.HasPrefix(value, "/") {
		return "", errors.New("ownership path must be repository-relative")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("ownership path escapes repository root")
	}
	return cleaned, nil
}

func statusOf(state State, id string) Status {
	if status, ok := state[id]; ok && status != "" {
		return status
	}
	return StatusPending
}

func dependencyFailed(task Task, state State) bool {
	for _, dependency := range task.DependsOn {
		switch statusOf(state, dependency) {
		case StatusFailed, StatusSkipped:
			return true
		}
	}
	return false
}

func dependenciesSucceeded(task Task, state State) bool {
	for _, dependency := range task.DependsOn {
		if statusOf(state, dependency) != StatusSuccess {
			return false
		}
	}
	return true
}

func conflictsAny(task Task, others []Task) bool {
	for _, other := range others {
		if OwnershipConflict(task.Owns, other.Owns) {
			return true
		}
	}
	return false
}

func SortedSkipped(decision Decision) []string {
	keys := make([]string, 0, len(decision.Skipped))
	for id := range decision.Skipped {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	return keys
}
