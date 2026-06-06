package models

import "testing"

func TestPRState_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{PRStateOpen, "open"},
		{PRStateClosed, "closed"},
		{PRStateMerged, "merged"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestPullRequest_IsOpen(t *testing.T) {
	t.Run("open PR", func(t *testing.T) {
		pr := &PullRequest{State: PRStateOpen}
		assertEqual(t, pr.IsOpen(), true)
	})

	t.Run("closed PR", func(t *testing.T) {
		pr := &PullRequest{State: PRStateClosed}
		assertEqual(t, pr.IsOpen(), false)
	})

	t.Run("merged PR", func(t *testing.T) {
		pr := &PullRequest{State: PRStateMerged}
		assertEqual(t, pr.IsOpen(), false)
	})
}

func TestPullRequest_IsMerged(t *testing.T) {
	t.Run("merged PR", func(t *testing.T) {
		pr := &PullRequest{State: PRStateMerged}
		assertEqual(t, pr.IsMerged(), true)
	})

	t.Run("open PR", func(t *testing.T) {
		pr := &PullRequest{State: PRStateOpen}
		assertEqual(t, pr.IsMerged(), false)
	})

	t.Run("closed PR", func(t *testing.T) {
		pr := &PullRequest{State: PRStateClosed}
		assertEqual(t, pr.IsMerged(), false)
	})
}
