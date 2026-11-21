package handlers

// This API imports advisory decisions made by the separate local PR Triage scanner.
// It does not invoke the scanner, access its loopback service, or authorize merges.
import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/go-chi/chi/v5"
)

const maxPRTriageImportBytes = 2 << 20
const maxPRTriageDecisions = 500

type importedPRTriageSnapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Warnings    []string  `json:"warnings"`
	Decisions   []struct {
		PR struct {
			Repo    string `json:"repo"`
			Number  int    `json:"number"`
			Title   string `json:"title"`
			URL     string `json:"url"`
			HeadSHA string `json:"headRefOid"`
			Base    string `json:"baseRefName"`
			Draft   bool   `json:"isDraft"`
		} `json:"pr"`
		Bucket       string   `json:"bucket"`
		Score        int      `json:"score"`
		Reasons      []string `json:"reasons"`
		Dependencies []string `json:"dependencies"`
	} `json:"decisions"`
}

var triageBuckets = map[string]bool{
	"candidate": true, "review": true, "draft": true,
	"dependency-blocked": true, "ci-blocked": true, "needs-repair": true,
}

func validHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func validPRTriageSnapshot(s importedPRTriageSnapshot, now time.Time) error {
	if s.GeneratedAt.IsZero() || s.GeneratedAt.After(now.Add(5*time.Minute)) || now.Sub(s.GeneratedAt) > 48*time.Hour {
		return errors.New("snapshot timestamp missing, expired, or from the future")
	}
	if len(s.Decisions) == 0 || len(s.Decisions) > maxPRTriageDecisions {
		return errors.New("snapshot must contain between 1 and 500 decisions")
	}
	seen := map[string]bool{}
	for _, d := range s.Decisions {
		p := d.PR
		parts := strings.Split(p.Repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(p.Repo, " ?#\\") {
			return errors.New("invalid repository name")
		}
		if p.Number <= 0 || p.Number > 100000000 || len(p.Title) == 0 || len(p.Title) > 4096 || !validHexSHA(p.HeadSHA) ||
			len(p.Base) == 0 || len(p.Base) > 255 || !triageBuckets[d.Bucket] || d.Score < 0 || d.Score > 100000 {
			return errors.New("invalid PR triage decision")
		}
		u, err := url.Parse(p.URL)
		expectedPath := fmt.Sprintf("/%s/pull/%d", p.Repo, p.Number)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || !strings.EqualFold(u.Path, expectedPath) ||
			u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return errors.New("PR link is not a canonical GitHub PR URL")
		}
		if len(d.Reasons) > 32 || len(d.Dependencies) > 32 {
			return errors.New("too many PR annotations")
		}
		for _, reason := range append(append([]string{}, d.Reasons...), d.Dependencies...) {
			if len(reason) > 1000 {
				return errors.New("PR annotation exceeds size limit")
			}
		}
		key := strings.ToLower(fmt.Sprintf("%s#%d", p.Repo, p.Number))
		if seen[key] {
			return errors.New("duplicate PR in snapshot")
		}
		seen[key] = true
	}
	return nil
}

// ImportPRTriage accepts a bounded, complete scanner payload for already-connected
// repositories only. Caller must be an authorized organization administrator.
// The server does not trust "candidate" or "verified" flags as merge authorization.
func (h *Handler) ImportPRTriage(w http.ResponseWriter, r *http.Request) {
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}
	if err := authz.AuthorizeOrganization(r.Context(), h.db, user, orgID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}
	if !h.requireAdmin(w, user) {
		return
	}
	// The signed-in JWT can retain a stale admin role after demotion.
	// Require the current database role as well before accepting an import.
	var liveRole string
	if err := h.db.QueryRowContext(r.Context(),
		"SELECT role FROM users WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL",
		user.UserID, orgID).Scan(&liveRole); err != nil {
		respond.Error(w, http.StatusForbidden, errors.New("administrator membership could not be verified"))
		return
	}
	if liveRole != "admin" && liveRole != "owner" {
		respond.Error(w, http.StatusForbidden, errors.New("administrator access required"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPRTriageImportBytes)
	var snapshot importedPRTriageSnapshot
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&snapshot); err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid or oversized snapshot"))
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		respond.Error(w, http.StatusBadRequest, errors.New("snapshot must contain a single JSON object"))
		return
	}
	if err := validPRTriageSnapshot(snapshot, time.Now().UTC()); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
  SELECT r.id, r.full_name FROM repositories r
  JOIN projects p ON p.id = r.project_id
  WHERE p.organization_id = $1 AND p.deleted_at IS NULL AND r.deleted_at IS NULL
 `, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("repository lookup failed"))
		return
	}
	repoIDs := map[string]string{}
	ambiguous := map[string]bool{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			respond.Error(w, 500, errors.New("repository lookup failed"))
			return
		}
		key := strings.ToLower(name)
		if _, exists := repoIDs[key]; exists {
			ambiguous[key] = true
		}
		repoIDs[key] = id
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		respond.Error(w, 500, errors.New("repository lookup failed"))
		return
	}
	for _, d := range snapshot.Decisions {
		name := strings.ToLower(d.PR.Repo)
		if repoIDs[name] == "" || ambiguous[name] {
			respond.Error(w, http.StatusBadRequest, errors.New("snapshot includes an unconnected or ambiguous repository"))
			return
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		respond.Error(w, 500, errors.New("triage import transaction failed"))
		return
	}
	defer tx.Rollback()
	imported := 0
	for _, d := range snapshot.Decisions {
		reasons, _ := json.Marshal(nonNilStrings(d.Reasons))
		deps, _ := json.Marshal(nonNilStrings(d.Dependencies))
		result, err := tx.ExecContext(r.Context(), `
   INSERT INTO external_pr_triage
   (repository_id, organization_id, number, title, url, head_sha, base_branch, bucket, score, draft, reasons, dependencies, snapshot_at)
   SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
   FROM repositories r JOIN projects p ON p.id = r.project_id
   WHERE r.id = $1 AND p.organization_id = $2 AND r.deleted_at IS NULL AND p.deleted_at IS NULL
   ON CONFLICT (repository_id, number) DO UPDATE SET
    title = EXCLUDED.title, url = EXCLUDED.url, head_sha = EXCLUDED.head_sha,
    base_branch = EXCLUDED.base_branch, bucket = EXCLUDED.bucket,
    score = EXCLUDED.score, draft = EXCLUDED.draft, reasons = EXCLUDED.reasons,
    dependencies = EXCLUDED.dependencies, snapshot_at = EXCLUDED.snapshot_at,
    received_at = CURRENT_TIMESTAMP, organization_id = EXCLUDED.organization_id
   WHERE external_pr_triage.snapshot_at <= EXCLUDED.snapshot_at
  `, repoIDs[strings.ToLower(d.PR.Repo)], orgID, d.PR.Number, d.PR.Title, d.PR.URL,
			d.PR.HeadSHA, d.PR.Base, d.Bucket, d.Score, d.PR.Draft, string(reasons), string(deps), snapshot.GeneratedAt)
		if err != nil {
			respond.Error(w, 500, errors.New("triage import failed"))
			return
		}
		affected, err := result.RowsAffected()
		if err != nil {
			respond.Error(w, 500, errors.New("triage import result unknown"))
			return
		}
		// A repo disconnected during import must fail the entire transaction.
		if affected == 0 {
			// A newer snapshot can already exist, so do not claim this decision imported.
			continue
		}
		imported++
	}
	if err := tx.Commit(); err != nil {
		respond.Error(w, 500, errors.New("triage import commit failed"))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"imported": imported, "received": len(snapshot.Decisions), "warnings": len(snapshot.Warnings),
		"advisory_only": true,
	})
}

// SQLite returns TIMESTAMPTZ as a string, whereas PostgreSQL returns time.Time.
// Normalize both representations without relying on process-local time zones.
func parseTriageTimestamp(raw any) (time.Time, error) {
	switch v := raw.(type) {
	case time.Time:
		return v.UTC(), nil
	case []byte:
		return parseTriageTimestamp(string(v))
	case string:
		t, err := time.Parse(time.RFC3339Nano, strings.Replace(v, " ", "T", 1))
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unsupported triage timestamp")
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

type externalPRTriageItem struct {
	Repo         string    `json:"repo"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	HeadSHA      string    `json:"head_sha"`
	BaseBranch   string    `json:"base_branch"`
	Bucket       string    `json:"bucket"`
	Score        int       `json:"score"`
	Draft        bool      `json:"draft"`
	Reasons      []string  `json:"reasons"`
	Dependencies []string  `json:"dependencies"`
	SnapshotAt   time.Time `json:"snapshot_at"`
}

// ListPRTriage reads advisory snapshots only from live repositories belonging to
// the caller's organization. Missing data is never interpreted as merge readiness.
func (h *Handler) ListPRTriage(w http.ResponseWriter, r *http.Request) {
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, 400, errors.New("organization id is required"))
		return
	}
	if err := authz.AuthorizeOrganization(r.Context(), h.db, user, orgID); err != nil {
		respond.Error(w, 404, errors.New("organization not found"))
		return
	}
	q := r.URL.Query()
	limit := 50
	offset := 0
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			respond.Error(w, 400, errors.New("limit must be 1-100"))
			return
		}
		limit = n
	}
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 50000 {
			respond.Error(w, 400, errors.New("offset must be 0-50000"))
			return
		}
		offset = n
	}
	bucket := q.Get("bucket")
	if bucket != "" && !triageBuckets[bucket] {
		respond.Error(w, 400, errors.New("unknown triage bucket"))
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
  SELECT r.full_name, e.number, e.title, e.url, e.head_sha, e.base_branch,
         e.bucket, e.score, e.draft, e.reasons, e.dependencies, e.snapshot_at
  FROM external_pr_triage e
  JOIN repositories r ON r.id = e.repository_id AND r.deleted_at IS NULL
  JOIN projects p ON p.id = r.project_id AND p.deleted_at IS NULL
  WHERE p.organization_id = $1 AND e.organization_id = $1
   AND ($2 = '' OR e.bucket = $2)
   AND ($3 = '' OR LOWER(r.full_name) = LOWER($3))
  ORDER BY e.score DESC, e.snapshot_at DESC, r.full_name ASC, e.number ASC
  LIMIT $4 OFFSET $5
 `, orgID, bucket, q.Get("repo"), limit+1, offset)
	if err != nil {
		respond.Error(w, 500, errors.New("triage query failed"))
		return
	}
	defer rows.Close()
	items := []externalPRTriageItem{}
	for rows.Next() {
		var item externalPRTriageItem
		var reasons, deps []byte
		var rawSnapshotAt any
		if err := rows.Scan(&item.Repo, &item.Number, &item.Title, &item.URL, &item.HeadSHA,
			&item.BaseBranch, &item.Bucket, &item.Score, &item.Draft, &reasons, &deps, &rawSnapshotAt); err != nil {
			respond.Error(w, 500, errors.New("triage row invalid"))
			return
		}
		item.SnapshotAt, err = parseTriageTimestamp(rawSnapshotAt)
		if err != nil {
			respond.Error(w, 500, errors.New("triage timestamp invalid"))
			return
		}
		if err := json.Unmarshal(reasons, &item.Reasons); err != nil {
			respond.Error(w, 500, errors.New("triage reasons invalid"))
			return
		}
		if err := json.Unmarshal(deps, &item.Dependencies); err != nil {
			respond.Error(w, 500, errors.New("triage dependencies invalid"))
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		respond.Error(w, 500, errors.New("triage query incomplete"))
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "offset": offset, "has_more": more,
		"advisory_only": true, "completeness_unverified": true,
	})
}
