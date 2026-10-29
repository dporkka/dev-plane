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
	"github.com/ai-dev-control-plane/scheduler"
)

// SchedulingDiagnosticCheck is an observed blocker, never a run admission.
type SchedulingDiagnosticCheck struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// SchedulingDiagnostics describes a read-only database snapshot. Capacity and
// budgets belong to the worker and are deliberately unknown here; a clear
// ownership/dependency check cannot authorize or trigger execution.
type SchedulingDiagnostics struct {
	TaskID                 string                               `json:"task_id"`
	ObservedAt             time.Time                            `json:"observed_at"`
	Status                 string                               `json:"status"`
	AdmissionAuthoritative bool                                 `json:"admission_authoritative"`
	ActiveClaims           int                                  `json:"active_claims"`
	Checks                 map[string]SchedulingDiagnosticCheck `json:"checks"`
}

type diagnosticSchedulerConfig struct {
	Scheduler *struct {
		Owns      []string `json:"owns"`
		DependsOn []string `json:"depends_on"`
	} `json:"scheduler"`
}

const maxDiagnosticClaims = 200
const maxDiagnosticDependencies = 100

func diagnosticCheck(status, reason string) SchedulingDiagnosticCheck {
	return SchedulingDiagnosticCheck{Status: status, Reason: reason}
}

// GetTaskSchedulingDiagnostics is authenticated and read-only. It uses the
// worker's persisted scheduler metadata contract and the shared ownership
// predicate, without claiming to replay the worker's atomic admission checks.
func (h *Handler) GetTaskSchedulingDiagnostics(w http.ResponseWriter, r *http.Request) {
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}
	if err := authz.AuthorizeTask(r.Context(), h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}
	report, err := h.diagnoseTaskScheduling(r.Context(), taskID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("scheduling diagnostics unavailable"))
		return
	}
	respond.JSON(w, http.StatusOK, report)
}

func (h *Handler) diagnoseTaskScheduling(ctx context.Context, taskID string) (SchedulingDiagnostics, error) {
	result := SchedulingDiagnostics{
		TaskID:                 taskID,
		ObservedAt:             time.Now().UTC(),
		Status:                 "unknown",
		AdmissionAuthoritative: false,
		Checks: map[string]SchedulingDiagnosticCheck{
			"dependencies": diagnosticCheck("unknown", "not-evaluated"),
			"ownership":    diagnosticCheck("unknown", "not-evaluated"),
			"capacity":     diagnosticCheck("unknown", "worker-capacity-not-exposed"),
			"budget":       diagnosticCheck("unknown", "worker-budget-not-exposed"),
		},
	}
	var projectID, repositoryID, taskStatus, metadata string
	err := h.db.QueryRowContext(ctx, `
        SELECT project_id, repository_id, status, COALESCE(metadata, '{}')
        FROM tasks WHERE id = $1 AND deleted_at IS NULL
    `, taskID).Scan(&projectID, &repositoryID, &taskStatus, &metadata)
	if err != nil {
		return result, err
	}

	var config diagnosticSchedulerConfig
	configOK := json.Unmarshal([]byte(metadata), &config) == nil
	if configOK && config.Scheduler != nil && len(config.Scheduler.DependsOn) > maxDiagnosticDependencies {
		configOK = false
	}
	switch {
	case !configOK:
		result.Checks["dependencies"] = diagnosticCheck("unknown", "invalid-scheduler-metadata")
	case config.Scheduler == nil || len(config.Scheduler.DependsOn) == 0:
		result.Checks["dependencies"] = diagnosticCheck("clear", "no-dependencies-configured")
	default:
		check := diagnosticCheck("clear", "all-prerequisites-done")
		for _, dep := range config.Scheduler.DependsOn {
			if dep == "" || dep == taskID {
				check = diagnosticCheck("blocked", "invalid-dependency")
				break
			}
			var status string
			// Scope by project AND repository. Never echo out-of-scope IDs.
			err := h.db.QueryRowContext(ctx, `
                SELECT status FROM tasks
                WHERE id = $1 AND project_id = $2 AND repository_id = $3
                  AND deleted_at IS NULL
            `, dep, projectID, repositoryID).Scan(&status)
			if errors.Is(err, sql.ErrNoRows) {
				check = diagnosticCheck("blocked", "dependency-unavailable")
				break
			}
			if err != nil {
				return result, err
			}
			if status != "done" {
				check = diagnosticCheck("blocked", "dependency-not-done")
				break
			}
		}
		result.Checks["dependencies"] = check
	}

	// Matching the worker's project/repository claim scope; no cross-tenant
	// contenders are queried or returned. Bounded to avoid untrusted fan-out.
	rows, err := h.db.QueryContext(ctx, `
        SELECT t.id, COALESCE(t.metadata, '{}')
        FROM agent_runs ar JOIN tasks t ON t.id = ar.task_id
        WHERE ar.status IN ('admitting','running') AND t.project_id = $1
          AND t.repository_id = $2 AND t.id <> $3 AND t.deleted_at IS NULL
        ORDER BY t.id LIMIT 201
    `, projectID, repositoryID, taskID)
	if err != nil {
		return result, err
	}
	type claim struct{ id, metadata string }
	claims := make([]claim, 0)
	for rows.Next() {
		var item claim
		if err := rows.Scan(&item.id, &item.metadata); err != nil {
			_ = rows.Close()
			return result, err
		}
		claims = append(claims, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	result.ActiveClaims = len(claims)

	switch {
	case len(claims) > maxDiagnosticClaims:
		result.Checks["ownership"] = diagnosticCheck("unknown", "claim-snapshot-truncated")
	case !configOK:
		result.Checks["ownership"] = diagnosticCheck("unknown", "invalid-scheduler-metadata")
	default:
		var owns []string
		if config.Scheduler != nil {
			owns = config.Scheduler.Owns
		} else {
			owns, err = h.diagnosticSpecOwnership(ctx, taskID)
			if err != nil {
				return result, err
			}
		}
		if len(owns) == 0 {
			result.Checks["ownership"] = diagnosticCheck("unknown", "ownership-not-configured")
		} else {
			result.Checks["ownership"] = diagnosticCheck("clear", "no-observed-conflicts")
			for _, running := range claims {
				var other diagnosticSchedulerConfig
				if err := json.Unmarshal([]byte(running.metadata), &other); err != nil {
					result.Checks["ownership"] = diagnosticCheck("unknown", "running-ownership-unavailable")
					break
				}
				var otherOwns []string
				if other.Scheduler != nil {
					otherOwns = other.Scheduler.Owns
				} else {
					otherOwns, err = h.diagnosticSpecOwnership(ctx, running.id)
					if err != nil {
						return result, err
					}
				}
				if len(otherOwns) == 0 {
					result.Checks["ownership"] = diagnosticCheck("unknown", "running-ownership-unavailable")
					break
				}
				if scheduler.OwnershipConflict(owns, otherOwns) {
					result.Checks["ownership"] = diagnosticCheck("blocked", "ownership-conflict")
					break
				}
			}
		}
	}
	if result.Checks["dependencies"].Status == "blocked" || result.Checks["ownership"].Status == "blocked" {
		result.Status = "blocked"
	}
	// No write, claim, budget consumption, run transition, or admission result.
	return result, nil
}

func (h *Handler) diagnosticSpecOwnership(ctx context.Context, taskID string) ([]string, error) {
	var changed, created string
	err := h.db.QueryRowContext(ctx, `
        SELECT COALESCE(files_to_change, '[]'), COALESCE(files_to_create, '[]')
        FROM task_specs WHERE task_id = $1 LIMIT 1
    `, taskID).Scan(&changed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var filesChanged, filesCreated []string
	if json.Unmarshal([]byte(changed), &filesChanged) != nil ||
		json.Unmarshal([]byte(created), &filesCreated) != nil {
		return nil, nil // malformed spec is unknown, never a clear admission
	}
	return append(filesChanged, filesCreated...), nil
}
