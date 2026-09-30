package repoprotocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func (w WorkItem) HasActiveLease(now time.Time) bool {
	return strings.TrimSpace(w.ClaimedBy) != "" &&
		w.LeaseUntil != nil &&
		now.Before(*w.LeaseUntil)
}

func (w WorkItem) ClaimableAt(now time.Time) bool {
	switch w.State {
	case WorkReady:
		return true
	case WorkClaimed:
		return !w.HasActiveLease(now)
	default:
		return false
	}
}

func (w *WorkItem) Claim(holder string, now time.Time, ttl time.Duration) error {
	if w == nil {
		return errors.New("work item is nil")
	}
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return errors.New("claim holder is required")
	}
	if ttl <= 0 {
		return errors.New("claim ttl must be positive")
	}
	if w.State == WorkClaimed && w.HasActiveLease(now) {
		return fmt.Errorf("work item %s already has an active lease held by %s", w.ID, w.ClaimedBy)
	}
	if !w.ClaimableAt(now) {
		return fmt.Errorf("work item %s is not claimable from state %s", w.ID, w.State)
	}

	expiresAt := now.Add(ttl)
	w.State = WorkClaimed
	w.ClaimedBy = holder
	w.LeaseUntil = &expiresAt
	return nil
}
