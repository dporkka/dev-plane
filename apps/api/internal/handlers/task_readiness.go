package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/readiness"
)

// GetTaskReadiness evaluates whether an existing task specification contains
// enough deterministic evidence for autonomous execution. It is advisory: the
// endpoint does not change task status or admit/deny a run.
func (h *Handler) GetTaskReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}
	if err := authz.AuthorizeTask(ctx, h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	var repositoryID, riskLevel string
	err := h.db.QueryRowContext(ctx, `
		SELECT repository_id, risk_level FROM tasks
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&repositoryID, &riskLevel)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	input := readiness.TaskAssessmentInput{RiskLevel: riskLevel}
	var implementationPlan, filesToChange, filesToCreate, acceptanceCriteria sql.NullString
	var testPlan, rollbackPlan, requiredApprovals sql.NullString
	err = h.db.QueryRowContext(ctx, `
		SELECT implementation_plan, files_to_change, files_to_create, acceptance_criteria,
		       test_plan, rollback_plan, required_approvals
		FROM task_specs
		WHERE task_id = $1
	`, taskID).Scan(
		&implementationPlan,
		&filesToChange,
		&filesToCreate,
		&acceptanceCriteria,
		&testPlan,
		&rollbackPlan,
		&requiredApprovals,
	)
	if errors.Is(err, sql.ErrNoRows) {
		respond.JSON(w, http.StatusOK, readiness.AssessTask(input))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	input.HasSpec = true
	input.ImplementationPlan = decodeReadinessList(implementationPlan)
	input.FilesToChange = decodeReadinessList(filesToChange)
	input.FilesToCreate = decodeReadinessList(filesToCreate)
	input.AcceptanceCriteria = decodeReadinessList(acceptanceCriteria)
	input.RequiredApprovals = decodeReadinessList(requiredApprovals)
	if testPlan.Valid {
		input.TestPlan = testPlan.String
	}
	if rollbackPlan.Valid {
		input.RollbackPlan = rollbackPlan.String
	}

	if strings.TrimSpace(input.TestPlan) == "" {
		var testCommand, lintCommand, typecheckCommand, buildCommand sql.NullString
		err = h.db.QueryRowContext(ctx, `
			SELECT test_command, lint_command, typecheck_command, build_command
			FROM project_configs
			WHERE repository_id = $1
			ORDER BY updated_at DESC
			LIMIT 1
		`, repositoryID).Scan(&testCommand, &lintCommand, &typecheckCommand, &buildCommand)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if testCommand.Valid {
			input.TestCommand = testCommand.String
		}
		if lintCommand.Valid {
			input.LintCommand = lintCommand.String
		}
		if typecheckCommand.Valid {
			input.TypecheckCommand = typecheckCommand.String
		}
		if buildCommand.Valid {
			input.BuildCommand = buildCommand.String
		}
	}

	respond.JSON(w, http.StatusOK, readiness.AssessTask(input))
}

func decodeReadinessList(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw.String), &values); err != nil {
		return nil
	}
	return values
}
