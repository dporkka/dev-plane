package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestListAuditLogsRequiresAdmin(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{"owner can list", "owner", http.StatusOK},
		{"admin can list", "admin", http.StatusOK},
		{"member cannot list", "member", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock, cleanup := setupTest(t)
			defer cleanup()

			expectAuthorizeOrganization(mock, "org-1")
			if tt.wantStatus == http.StatusOK {
				rows := sqlmock.NewRows([]string{"id", "organization_id", "actor_type", "actor_id", "action", "resource_type", "resource_id", "details", "ip_address", "user_agent", "created_at"})
				mock.ExpectQuery("SELECT id, organization_id, actor_type, actor_id, action, resource_type").
					WithArgs("org-1", 50).
					WillReturnRows(rows)
			}

			req := httptest.NewRequest(http.MethodGet, "/organizations/org-1/audit-logs", nil)
			req = req.WithContext(withRole(req.Context(), tt.role))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("orgID", "org-1")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()

			h.ListAuditLogs(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestListAuditLogsReturnsLogs(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizeOrganization(mock, "org-1")
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "organization_id", "actor_type", "actor_id", "action", "resource_type", "resource_id", "details", "ip_address", "user_agent", "created_at"}).
		AddRow("audit-1", "org-1", "human", "user-1", "login", "user", "user-1", nil, nil, nil, now)
	mock.ExpectQuery("SELECT id, organization_id, actor_type, actor_id, action, resource_type").
		WithArgs("org-1", 50).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/organizations/org-1/audit-logs", nil)
	req = req.WithContext(withRole(req.Context(), "admin"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", "org-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListAuditLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var logs []AuditLog
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Action != "login" {
		t.Errorf("action = %q, want login", logs[0].Action)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
