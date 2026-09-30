package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

type TaskExecutionEvidenceResponse struct {
	Runs map[string]RunExecutionEvidence `json:"runs"`
}

type RunExecutionEvidence struct {
	RunID        string                   `json:"run_id"`
	CommitHash   string                   `json:"commit_hash,omitempty"`
	Verification *RunVerificationEvidence `json:"verification,omitempty"`
	Review       *RunReviewEvidence       `json:"review,omitempty"`
	PullRequest  *RunPullRequestEvidence  `json:"pull_request,omitempty"`
	Artifacts    []RunArtifactEvidence    `json:"artifacts"`
	Failure      *RunFailureEvidence      `json:"failure,omitempty"`
}

type RunVerificationEvidence struct {
	StepID     string `json:"step_id"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome,omitempty"`
	Passed     bool   `json:"passed"`
	Total      int    `json:"total"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	DurationMs int    `json:"duration_ms"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

type RunReviewEvidence struct {
	Summary       string              `json:"summary"`
	RiskLevel     string              `json:"risk_level"`
	Approvable    bool                `json:"approvable"`
	Findings      json.RawMessage     `json:"findings,omitempty"`
	Suggestions   json.RawMessage     `json:"suggestions,omitempty"`
	TestCoverage  string              `json:"test_coverage,omitempty"`
	SecurityNotes string              `json:"security_notes,omitempty"`
	DiffSummary   EvidenceDiffSummary `json:"diff_summary"`
	CreatedAt     time.Time           `json:"created_at"`
}

type EvidenceDiffSummary struct {
	FilesChanged int                  `json:"files_changed"`
	Insertions   int                  `json:"insertions"`
	Deletions    int                  `json:"deletions"`
	Files        []EvidenceFileChange `json:"files,omitempty"`
}

type EvidenceFileChange struct {
	Path        string `json:"path"`
	Status      string `json:"status"`
	Insertions  int    `json:"insertions"`
	Deletions   int    `json:"deletions"`
	IsTest      bool   `json:"is_test"`
	IsConfig    bool   `json:"is_config"`
	IsMigration bool   `json:"is_migration"`
}

type RunPullRequestEvidence struct {
	ID        string    `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	CreatedAt time.Time `json:"created_at"`
}

type RunArtifactEvidence struct {
	ID           string          `json:"id"`
	ArtifactType string          `json:"artifact_type"`
	FileName     string          `json:"file_name"`
	MimeType     string          `json:"mime_type,omitempty"`
	SizeBytes    int64           `json:"size_bytes,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

type RunFailureEvidence struct {
	Status  string `json:"status"`
	Outcome string `json:"outcome,omitempty"`
	Message string `json:"message"`
	Summary string `json:"summary,omitempty"`
}

func (h *Handler) ListTaskExecutionEvidence(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}
	if err := authz.AuthorizeTask(ctx, h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	response := TaskExecutionEvidenceResponse{Runs: map[string]RunExecutionEvidence{}}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, status, outcome, error_message, summary
		FROM agent_runs
		WHERE task_id = $1
	`, taskID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	for rows.Next() {
		var runID, status string
		var outcome, errorMessage, summary sql.NullString
		if err := rows.Scan(&runID, &status, &outcome, &errorMessage, &summary); err != nil {
			_ = rows.Close()
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		evidence := RunExecutionEvidence{RunID: runID, Artifacts: []RunArtifactEvidence{}}
		if errorMessage.Valid && errorMessage.String != "" {
			evidence.Failure = &RunFailureEvidence{
				Status:  status,
				Message: errorMessage.String,
			}
			if outcome.Valid {
				evidence.Failure.Outcome = outcome.String
			}
			if summary.Valid {
				evidence.Failure.Summary = summary.String
			}
		}
		response.Runs[runID] = evidence
	}
	if err := rows.Close(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := rows.Err(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.loadExecutionStepEvidence(ctx, taskID, response.Runs); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.loadReviewEvidence(ctx, taskID, response.Runs); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.loadPullRequestEvidence(ctx, taskID, response.Runs); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.loadArtifactEvidence(ctx, taskID, response.Runs); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusOK, response)
}

func (h *Handler) loadExecutionStepEvidence(ctx context.Context, taskID string, runs map[string]RunExecutionEvidence) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT s.id, s.agent_run_id, s.tool_name, s.status, s.outcome,
		       s.output, s.tool_output, s.exit_code, s.created_at
		FROM agent_steps s
		JOIN agent_runs ar ON ar.id = s.agent_run_id
		WHERE ar.task_id = $1
		  AND s.tool_name IN ('create_commit', 'run_tests')
		ORDER BY s.created_at ASC, s.step_number ASC
	`, taskID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var stepID, runID, toolName, status string
		var outcome, output, toolOutput sql.NullString
		var exitCode sql.NullInt32
		var createdAt time.Time
		if err := rows.Scan(&stepID, &runID, &toolName, &status, &outcome, &output, &toolOutput, &exitCode, &createdAt); err != nil {
			return err
		}
		evidence, ok := runs[runID]
		if !ok {
			continue
		}
		payload := output.String
		if !output.Valid || payload == "" {
			payload = toolOutput.String
		}
		var values map[string]any
		if payload != "" {
			_ = json.Unmarshal([]byte(payload), &values)
		}

		switch toolName {
		case "create_commit":
			if hash, ok := values["commit_hash"].(string); ok && hash != "" {
				evidence.CommitHash = hash
			}
		case "run_tests":
			verification := &RunVerificationEvidence{
				StepID: stepID,
				Status: status,
			}
			if outcome.Valid {
				verification.Outcome = outcome.String
				verification.Passed = outcome.String == "passed"
			}
			if passed, ok := values["passed"].(bool); ok {
				verification.Passed = passed
			}
			verification.Total = jsonInt(values["total"])
			verification.Failed = jsonInt(values["failed"])
			verification.Skipped = jsonInt(values["skipped"])
			verification.DurationMs = jsonInt(values["duration_ms"])
			if exitCode.Valid {
				code := int(exitCode.Int32)
				verification.ExitCode = &code
			} else if value, ok := values["exit_code"]; ok {
				code := jsonInt(value)
				verification.ExitCode = &code
			}
			evidence.Verification = verification
		}
		runs[runID] = evidence
	}
	return rows.Err()
}

func (h *Handler) loadReviewEvidence(ctx context.Context, taskID string, runs map[string]RunExecutionEvidence) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT rr.run_id, rr.summary, rr.risk_level, rr.approvable,
		       rr.findings, rr.suggestions, rr.test_coverage, rr.security_notes,
		       rr.diff_summary, rr.created_at
		FROM review_reports rr
		JOIN agent_runs ar ON ar.id = rr.run_id
		WHERE ar.task_id = $1
		ORDER BY rr.created_at ASC
	`, taskID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var runID, summary, riskLevel, testCoverage, securityNotes string
		var approvable bool
		var findings, suggestions, diffSummary sql.NullString
		var createdAt time.Time
		if err := rows.Scan(
			&runID, &summary, &riskLevel, &approvable,
			&findings, &suggestions, &testCoverage, &securityNotes,
			&diffSummary, &createdAt,
		); err != nil {
			return err
		}
		evidence, ok := runs[runID]
		if !ok {
			continue
		}
		review := &RunReviewEvidence{
			Summary:       summary,
			RiskLevel:     riskLevel,
			Approvable:    approvable,
			TestCoverage:  testCoverage,
			SecurityNotes: securityNotes,
			CreatedAt:     createdAt,
		}
		if findings.Valid {
			review.Findings = json.RawMessage(findings.String)
		}
		if suggestions.Valid {
			review.Suggestions = json.RawMessage(suggestions.String)
		}
		if diffSummary.Valid && diffSummary.String != "" {
			_ = json.Unmarshal([]byte(diffSummary.String), &review.DiffSummary)
		}
		evidence.Review = review
		runs[runID] = evidence
	}
	return rows.Err()
}

func (h *Handler) loadPullRequestEvidence(ctx context.Context, taskID string, runs map[string]RunExecutionEvidence) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT run_id, id, number, title, url, state, draft, created_at
		FROM pull_requests
		WHERE task_id = $1 AND run_id IS NOT NULL
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var runID string
		var pr RunPullRequestEvidence
		if err := rows.Scan(&runID, &pr.ID, &pr.Number, &pr.Title, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedAt); err != nil {
			return err
		}
		evidence, ok := runs[runID]
		if !ok {
			continue
		}
		evidence.PullRequest = &pr
		runs[runID] = evidence
	}
	return rows.Err()
}

func (h *Handler) loadArtifactEvidence(ctx context.Context, taskID string, runs map[string]RunExecutionEvidence) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT a.agent_run_id, a.id, a.artifact_type, a.file_name,
		       a.mime_type, a.size_bytes, a.metadata, a.created_at
		FROM artifacts a
		JOIN agent_runs ar ON ar.id = a.agent_run_id
		WHERE ar.task_id = $1 AND a.agent_run_id IS NOT NULL
		ORDER BY a.created_at ASC
	`, taskID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var runID string
		var artifact RunArtifactEvidence
		var mimeType, metadata sql.NullString
		var sizeBytes sql.NullInt64
		if err := rows.Scan(
			&runID, &artifact.ID, &artifact.ArtifactType, &artifact.FileName,
			&mimeType, &sizeBytes, &metadata, &artifact.CreatedAt,
		); err != nil {
			return err
		}
		if mimeType.Valid {
			artifact.MimeType = mimeType.String
		}
		if sizeBytes.Valid {
			artifact.SizeBytes = sizeBytes.Int64
		}
		if metadata.Valid {
			artifact.Metadata = json.RawMessage(metadata.String)
		}
		evidence, ok := runs[runID]
		if !ok {
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, artifact)
		runs[runID] = evidence
	}
	return rows.Err()
}

func jsonInt(value any) int {
	switch n := value.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		v, _ := n.Int64()
		return int(v)
	default:
		return 0
	}
}
