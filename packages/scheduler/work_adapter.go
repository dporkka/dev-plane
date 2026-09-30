package scheduler

import (
	"errors"
	"fmt"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

type WorkScheduleOptions struct {
	MaxParallelCost int
	Now             time.Time
}

// NextWork adapts durable repository-protocol work items to the existing
// scheduler without creating a second scheduling algorithm.
//
// WorkItem.Cost is encoded into the scheduler's CPU capacity dimension for this
// adapter only. Memory is neutralized to one unit per item, so readiness remains
// governed by dependency state, ownership conflicts, active work, and the
// repository-level parallel cost budget.
func NextWork(items []repoprotocol.WorkItem, options WorkScheduleOptions) (Decision, error) {
	if len(items) == 0 {
		return Decision{}, errors.New("work scheduler requires at least one work item")
	}
	if options.MaxParallelCost <= 0 {
		return Decision{}, errors.New("max_parallel_cost must be positive")
	}
	if options.Now.IsZero() {
		return Decision{}, errors.New("work scheduler requires a deterministic current time")
	}

	manifest := Manifest{
		MaxParallel: len(items),
		Capacity: Capacity{
			CPU:      float64(options.MaxParallelCost),
			MemoryMB: len(items),
		},
		Tasks: make([]Task, 0, len(items)),
	}
	state := make(State, len(items))

	for _, item := range items {
		if err := item.Validate(); err != nil {
			return Decision{}, fmt.Errorf("work item %s: %w", item.ID, err)
		}
		if item.State == repoprotocol.WorkDraft {
			return Decision{}, fmt.Errorf("draft work item %s is not schedulable", item.ID)
		}

		manifest.Tasks = append(manifest.Tasks, Task{
			ID:        item.ID,
			Owns:      append([]string(nil), item.OwnershipPaths...),
			DependsOn: append([]string(nil), item.DependsOn...),
			Resources: Resources{
				CPU:      float64(item.Cost),
				MemoryMB: 1,
			},
		})

		switch item.State {
		case repoprotocol.WorkReady:
			state[item.ID] = StatusPending
		case repoprotocol.WorkClaimed:
			if item.HasActiveLease(options.Now) {
				state[item.ID] = StatusRunning
			} else {
				state[item.ID] = StatusPending
			}
		case repoprotocol.WorkImplementing,
			repoprotocol.WorkVerifying,
			repoprotocol.WorkReviewing,
			repoprotocol.WorkReadyToLand:
			state[item.ID] = StatusRunning
		case repoprotocol.WorkLanded:
			state[item.ID] = StatusSuccess
		case repoprotocol.WorkFailed:
			state[item.ID] = StatusFailed
		case repoprotocol.WorkCancelled:
			state[item.ID] = StatusSkipped
		default:
			return Decision{}, fmt.Errorf("work item %s has unsupported scheduling state %s", item.ID, item.State)
		}
	}

	return Next(manifest, state)
}
