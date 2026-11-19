package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/go-chi/chi/v5"
)

var browserAPIKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// BrowserJobHandler only queues validation-only jobs. No browser is executed
// in the API process; a separate authenticated worker adapter is required.
type BrowserJobHandler struct {
	*Handler
	store          *dbpkg.BrowserJobStore
	allowedOrigins map[string]struct{}
}

type browserCheck struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type browserCapture struct {
	Screenshot bool `json:"screenshot"`
	Trace      bool `json:"trace"`
}

type browserJobRequest struct {
	IdempotencyKey string         `json:"idempotency_key"`
	StartURL       string         `json:"start_url"`
	Checks         []browserCheck `json:"checks"`
	TimeoutMS      int            `json:"timeout_ms"`
	Capture        browserCapture `json:"capture"`
}

type browserJobSpec struct {
	Version   int            `json:"version"`
	TenantID  string         `json:"tenant_id"`
	StartURL  string         `json:"start_url"`
	Checks    []browserCheck `json:"checks"`
	TimeoutMS int            `json:"timeout_ms"`
	Capture   browserCapture `json:"capture"`
}

type browserJobResponse struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	State          string          `json:"state"`
	Result         json.RawMessage `json:"result,omitempty"`
}

func NewBrowserJobHandler(database *sql.DB, origins []string) *BrowserJobHandler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			continue
		}
		allowed[u.Scheme+"://"+strings.ToLower(u.Host)] = struct{}{}
	}
	return &BrowserJobHandler{
		Handler:        NewHandler(database, nil),
		store:          dbpkg.NewBrowserJobStore(database),
		allowedOrigins: allowed,
	}
}

func (h *BrowserJobHandler) authorized(w http.ResponseWriter, r *http.Request, admin bool) (string, bool) {
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return "", false
	}
	org := chi.URLParam(r, "orgID")
	if org == "" || org != user.OrgID {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return "", false
	}
	if err := authz.AuthorizeOrganization(r.Context(), h.db, user, org); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return "", false
	}
	if admin && !h.requireAdmin(w, user) {
		return "", false
	}
	return org, true
}

func (h *BrowserJobHandler) validate(req browserJobRequest, org string) (browserJobSpec, error) {
	if !browserAPIKeyPattern.MatchString(req.IdempotencyKey) {
		return browserJobSpec{}, errors.New("invalid idempotency_key")
	}
	if len(h.allowedOrigins) == 0 {
		return browserJobSpec{}, errors.New("browser verification disabled: operator origin allowlist is empty")
	}
	u, err := url.Parse(req.StartURL)
	if err != nil || len(req.StartURL) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return browserJobSpec{}, errors.New("invalid HTTPS start_url")
	}
	if _, ok := h.allowedOrigins[u.Scheme+"://"+strings.ToLower(u.Host)]; !ok {
		return browserJobSpec{}, errors.New("start_url not operator allowlisted")
	}
	if req.TimeoutMS < 1000 || req.TimeoutMS > 120000 {
		return browserJobSpec{}, errors.New("timeout_ms must be between 1000 and 120000")
	}
	if len(req.Checks) == 0 || len(req.Checks) > 20 {
		return browserJobSpec{}, errors.New("checks must have between 1 and 20 assertions")
	}
	for _, check := range req.Checks {
		if check.Type != "selector_visible" && check.Type != "text_visible" {
			return browserJobSpec{}, errors.New("unsupported check type")
		}
		if len(check.Value) == 0 || len(check.Value) > 500 {
			return browserJobSpec{}, errors.New("invalid check value")
		}
	}
	return browserJobSpec{Version: 1, TenantID: org, StartURL: u.String(), Checks: req.Checks, TimeoutMS: req.TimeoutMS, Capture: req.Capture}, nil
}

// Create enqueues an authenticated, tenant-bound read-only browser verification.
func (h *BrowserJobHandler) Create(w http.ResponseWriter, r *http.Request) {
	org, ok := h.authorized(w, r, true)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var input browserJobRequest
	if err := dec.Decode(&input); err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid browser job request"))
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid browser job request"))
		return
	}
	spec, err := h.validate(input, org)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("failed to encode job"))
		return
	}
	job, created, err := h.store.SubmitBrowserJob(r.Context(), org, input.IdempotencyKey, payload)
	if errors.Is(err, dbpkg.ErrBrowserJobConflict) {
		respond.Error(w, http.StatusConflict, errors.New("idempotency key reused with different job"))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("unable to store browser job"))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	respond.JSON(w, status, browserJobResponse{ID: job.ID, OrganizationID: job.OrganizationID, State: job.State})
}

// Get returns a tenant-scoped job status without exposing worker claim secrets.
func (h *BrowserJobHandler) Get(w http.ResponseWriter, r *http.Request) {
	org, ok := h.authorized(w, r, false)
	if !ok {
		return
	}
	job, err := h.store.GetBrowserJob(r.Context(), org, chi.URLParam(r, "id"))
	if errors.Is(err, dbpkg.ErrBrowserJobNotFound) {
		respond.Error(w, http.StatusNotFound, errors.New("browser job not found"))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("unable to load browser job"))
		return
	}
	respond.JSON(w, http.StatusOK, browserJobResponse{ID: job.ID, OrganizationID: job.OrganizationID, State: job.State, Result: job.Result})
}
