package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/respond"
)

// AuditLog represents an audit log record.
type AuditLog struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"organization_id"`
	ActorType    string          `json:"actor_type"`
	ActorID      *string         `json:"actor_id,omitempty"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   *string         `json:"resource_id,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
	IPAddress    *string         `json:"ip_address,omitempty"`
	UserAgent    *string         `json:"user_agent,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ListAuditLogs returns audit logs for an organization.
func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, organization_id, actor_type, actor_id, action, resource_type, resource_id,
		       details, ip_address, user_agent, created_at
		FROM audit_logs
		WHERE organization_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var l AuditLog
		var actorID, resourceID sql.NullString
		var details, ipAddress, userAgent sql.NullString

		err := rows.Scan(
			&l.ID, &l.OrgID, &l.ActorType, &actorID, &l.Action, &l.ResourceType, &resourceID,
			&details, &ipAddress, &userAgent, &l.CreatedAt,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if actorID.Valid {
			l.ActorID = &actorID.String
		}
		if resourceID.Valid {
			l.ResourceID = &resourceID.String
		}
		if details.Valid {
			l.Details = json.RawMessage(details.String)
		}
		if ipAddress.Valid {
			l.IPAddress = &ipAddress.String
		}
		if userAgent.Valid {
			l.UserAgent = &userAgent.String
		}
		logs = append(logs, l)
	}

	if logs == nil {
		logs = []AuditLog{}
	}
	respond.JSON(w, http.StatusOK, logs)
}
