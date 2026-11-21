package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// ReviewMetrics describes persisted reviewer findings linked to PRs *created*
// in a UTC date window. A zero count with reviewed_prs=0 means no linked
// review reports, not that there were no findings in other review systems.
type ReviewMetrics struct {
	ReviewedPRs      int64 `json:"reviewed_prs"`
	ApprovablePRs    int64 `json:"approvable_prs"`
	CriticalFindings int64 `json:"critical_findings"`
	HighFindings     int64 `json:"high_findings"`
	MediumFindings   int64 `json:"medium_findings"`
	LowFindings      int64 `json:"low_findings"`
	InfoFindings     int64 `json:"info_findings"`
	SecurityFindings int64 `json:"security_findings"`
}

// GetReviewMetrics aggregates existing review_reports. It does not manufacture
// comment acceptance, review duration, or time-saved estimates.
func (h *Handler) GetReviewMetrics(w http.ResponseWriter, r *http.Request) {
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}
	startText, endText := r.URL.Query().Get("start"), r.URL.Query().Get("end")
	start, startErr := time.Parse("2006-01-02", startText)
	end, endErr := time.Parse("2006-01-02", endText)
	if startErr != nil || endErr != nil || end.Before(start) {
		respond.Error(w, http.StatusBadRequest, errors.New("valid start and end dates in YYYY-MM-DD order are required"))
		return
	}
	var repoArg interface{}
	if repository := r.URL.Query().Get("repository"); repository != "" {
		parsed, err := uuid.Parse(repository)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, errors.New("repository must be a UUID"))
			return
		}
		repoArg = parsed.String()
	}
	ctx := r.Context()
	if err := authz.AuthorizeOrganization(ctx, h.db, user, orgID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}
	// One query produces all counters. Each PR with a linked review contributes
	// exactly once to reviewed_prs; findings only contribute to finding counters.
	const query = `WITH selected AS (
   SELECT rr.id, rr.approvable, rr.findings
   FROM pull_requests pr
   JOIN repositories repo ON repo.id = pr.repository_id
   JOIN projects project ON project.id = repo.project_id
   JOIN review_reports rr ON rr.run_id = pr.run_id
   WHERE project.organization_id = $1
     AND project.deleted_at IS NULL AND repo.deleted_at IS NULL
     AND pr.created_at >= $2 AND pr.created_at < $3
     AND ($4::uuid IS NULL OR pr.repository_id = $4::uuid)
  ), issues AS (
    SELECT issue.value AS finding FROM selected s
    CROSS JOIN LATERAL jsonb_array_elements(
      CASE WHEN jsonb_typeof(s.findings) = 'array'
      THEN s.findings ELSE '[]'::jsonb END
    ) AS issue(value)
  )
  SELECT
    (SELECT COUNT(*) FROM selected),
    (SELECT COUNT(*) FROM selected WHERE approvable),
    (SELECT COUNT(*) FROM issues WHERE LOWER(finding->>'severity') = 'critical'),
    (SELECT COUNT(*) FROM issues WHERE LOWER(finding->>'severity') = 'high'),
    (SELECT COUNT(*) FROM issues WHERE LOWER(finding->>'severity') = 'medium'),
    (SELECT COUNT(*) FROM issues WHERE LOWER(finding->>'severity') = 'low'),
    (SELECT COUNT(*) FROM issues WHERE LOWER(finding->>'severity') = 'info'),
    (SELECT COUNT(*) FROM issues WHERE LOWER(finding->>'category') = 'security')`
	var metrics ReviewMetrics
	err := h.db.QueryRowContext(ctx, query, orgID, start, end.AddDate(0, 0, 1), repoArg).Scan(
		&metrics.ReviewedPRs, &metrics.ApprovablePRs,
		&metrics.CriticalFindings, &metrics.HighFindings,
		&metrics.MediumFindings, &metrics.LowFindings,
		&metrics.InfoFindings, &metrics.SecurityFindings,
	)
	if err != nil {
		h.logger.Error("review metrics query failed", "error", err)
		respond.Error(w, http.StatusInternalServerError, errors.New("review metrics unavailable"))
		return
	}
	respond.JSON(w, http.StatusOK, metrics)
}
