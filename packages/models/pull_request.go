package models

import (
	"database/sql"
	"errors"
	"time"
)

// PRState constants for pull request lifecycle states.
const (
	PRStateOpen   = "open"
	PRStateClosed = "closed"
	PRStateMerged = "merged"
)

// PullRequest represents a GitHub pull request created by an agent run.
type PullRequest struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	RunID      *string    `json:"run_id,omitempty"`
	RepoID     string     `json:"repository_id"`
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Branch     string     `json:"branch"`
	BaseBranch string     `json:"base_branch"`
	URL        string     `json:"url"`
	State      string     `json:"state"` // open, closed, merged
	Draft      bool       `json:"draft"`
	CreatedBy  string     `json:"created_by"`
	MergedAt   *time.Time `json:"merged_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Validate checks that the pull request has required fields.
func (pr *PullRequest) Validate() error {
	if pr.TaskID == "" {
		return errors.New("pull_request task_id is required")
	}
	if pr.RepoID == "" {
		return errors.New("pull_request repository_id is required")
	}
	if pr.Number <= 0 {
		return errors.New("pull_request number must be positive")
	}
	if pr.Title == "" {
		return errors.New("pull_request title is required")
	}
	if pr.Branch == "" {
		return errors.New("pull_request branch is required")
	}
	if pr.BaseBranch == "" {
		return errors.New("pull_request base_branch is required")
	}
	return nil
}

// IsOpen returns true if the pull request is currently open.
func (pr *PullRequest) IsOpen() bool {
	return pr.State == PRStateOpen
}

// IsMerged returns true if the pull request has been merged.
func (pr *PullRequest) IsMerged() bool {
	return pr.State == PRStateMerged
}

// IsMergeable returns true if the pull request can be merged.
func (pr *PullRequest) IsMergeable() bool {
	return pr.State == PRStateOpen && !pr.Draft
}

// NullPullRequest returns a PullRequest from sql.Null fields.
func NullPullRequest(
	id sql.NullString,
	taskID sql.NullString,
	runID sql.NullString,
	repoID sql.NullString,
	number sql.NullInt32,
	title sql.NullString,
	body sql.NullString,
	branch sql.NullString,
	baseBranch sql.NullString,
	url sql.NullString,
	state sql.NullString,
	draft sql.NullBool,
	createdBy sql.NullString,
	mergedAt sql.NullTime,
	createdAt sql.NullTime,
	updatedAt sql.NullTime,
) *PullRequest {
	pr := &PullRequest{}
	if id.Valid {
		pr.ID = id.String
	}
	if taskID.Valid {
		pr.TaskID = taskID.String
	}
	if runID.Valid {
		r := runID.String
		pr.RunID = &r
	}
	if repoID.Valid {
		pr.RepoID = repoID.String
	}
	if number.Valid {
		pr.Number = int(number.Int32)
	}
	if title.Valid {
		pr.Title = title.String
	}
	if body.Valid {
		pr.Body = body.String
	}
	if branch.Valid {
		pr.Branch = branch.String
	}
	if baseBranch.Valid {
		pr.BaseBranch = baseBranch.String
	}
	if url.Valid {
		pr.URL = url.String
	}
	if state.Valid {
		pr.State = state.String
	}
	if draft.Valid {
		pr.Draft = draft.Bool
	}
	if createdBy.Valid {
		pr.CreatedBy = createdBy.String
	}
	if mergedAt.Valid {
		pr.MergedAt = &mergedAt.Time
	}
	if createdAt.Valid {
		pr.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		pr.UpdatedAt = updatedAt.Time
	}
	return pr
}
