package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/forgeexec"
	"github.com/ai-dev-control-plane/api/internal/forgereplay"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/api/internal/workloadauth"
	"github.com/ai-dev-control-plane/policies"
)

const maxForgeExecuteBody = 4 << 20

type ForgeExecuteRequest struct {
	RequestID string            `json:"request_id"`
	TaskID    string            `json:"task_id"`
	RunID     string            `json:"run_id"`
	Command   forgeexec.Command `json:"command"`
}

func (h *Handler) ExecuteForgeWorkload(w http.ResponseWriter, r *http.Request) {
	if h.workloadVerifier == nil || h.forgeReplayStore == nil || h.forgeExecutor == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("hosted forge execution is not configured"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxForgeExecuteBody+1))
	if err != nil || len(body) > maxForgeExecuteBody {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid forge execution body"))
		return
	}
	if err := h.workloadVerifier.Verify(r, body); err != nil {
		respond.Error(w, http.StatusUnauthorized, errors.New("invalid workload authorization"))
		return
	}

	input, err := decodeForgeExecuteRequest(body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	operation, err := input.Command.Operation()
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	ctx := r.Context()
	claim, err := h.forgeReplayStore.Claim(ctx, forgereplay.Request{
		ID: input.RequestID, WorkloadID: r.Header.Get(workloadauth.HeaderWorkload),
		TaskID: input.TaskID, RunID: input.RunID, Operation: operation, Body: body,
	})
	if err != nil {
		h.writeForgeClaimError(w, err)
		return
	}
	if h.handleForgeReplay(w, claim) {
		return
	}

	fctx, err := h.loadForgeContext(ctx, input.TaskID, input.RunID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.completeForgeError(w, ctx, input.RequestID, http.StatusNotFound, errors.New("forge task/run context not found"))
		} else {
			h.releaseForgeClaim(ctx, input.RequestID)
			respond.Error(w, http.StatusInternalServerError, errors.New("failed to load forge task/run context"))
		}
		return
	}
	if input.Command.Repository.Owner != fctx.Repository.Owner ||
		input.Command.Repository.Name != fctx.Repository.Name {
		h.completeForgeError(w, ctx, input.RequestID, http.StatusConflict,
			errors.New("forge command repository does not match canonical task repository"))
		return
	}

	decision, err := h.kernel().EvaluateForge(ctx, operation, capability.Request{
		ActorType: "agent", AgentRole: fctx.Run.AgentRole,
		Organization: fctx.Organization, Project: fctx.Project,
		Repository: fctx.Repository, Workspace: fctx.Workspace,
		Task: fctx.Task, AgentRun: fctx.Run, Resource: fctx.Repository.FullName,
		Details: map[string]any{
			"organization_id": fctx.Organization.ID, "task_id": fctx.Task.ID,
			"run_id": fctx.Run.ID, "agent_role": fctx.Run.AgentRole, "request_id": input.RequestID,
		},
	})
	if err != nil {
		if errors.Is(err, capability.ErrCapabilityUnknown) {
			h.completeForgeError(w, ctx, input.RequestID, http.StatusBadRequest, err)
		} else {
			h.releaseForgeClaim(ctx, input.RequestID)
			respond.Error(w, http.StatusInternalServerError, errors.New("forge capability evaluation failed"))
		}
		return
	}
	if decision.Effect != policies.EffectAllow {
		status, code := http.StatusForbidden, "policy_denied"
		if decision.RequiredApproval {
			status, code = http.StatusConflict, "approval_required"
		}
		h.completeForgeJSON(w, ctx, input.RequestID, status,
			map[string]string{"error": code, "reason": decision.Reason})
		return
	}

	result, err := h.forgeExecutor.Execute(ctx, input.Command)
	if err != nil {
		h.handleForgeProviderFailure(w, ctx, input, err)
		return
	}
	h.completeForgeExecution(w, ctx, input, result)
}

func decodeForgeExecuteRequest(body []byte) (ForgeExecuteRequest, error) {
	var input ForgeExecuteRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, fmt.Errorf("invalid forge execution request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return input, errors.New("request body must contain exactly one JSON value")
	}
	if input.RequestID == "" || input.TaskID == "" || input.RunID == "" {
		return input, errors.New("request_id, task_id, and run_id are required")
	}
	return input, nil
}

func (h *Handler) writeForgeClaimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, forgereplay.ErrConflict):
		respond.Error(w, http.StatusConflict, errors.New("request_id is bound to a different forge request"))
	case errors.Is(err, forgereplay.ErrInvalidRequest):
		respond.Error(w, http.StatusBadRequest, err)
	default:
		respond.Error(w, http.StatusInternalServerError, errors.New("failed to claim forge request"))
	}
}

func (h *Handler) handleForgeReplay(w http.ResponseWriter, claim forgereplay.Result) bool {
	switch claim.State {
	case forgereplay.StateReplay:
		writeForgeJSONBytes(w, claim.ResponseStatus, claim.ResponseBody)
		return true
	case forgereplay.StateInFlight:
		respond.Error(w, http.StatusConflict, errors.New("forge request is already in flight"))
		return true
	case forgereplay.StateUncertain:
		respond.Error(w, http.StatusConflict, errors.New("forge request outcome is uncertain and requires reconciliation"))
		return true
	case forgereplay.StateNew:
		return false
	default:
		respond.Error(w, http.StatusInternalServerError, errors.New("invalid forge replay state"))
		return true
	}
}

func (h *Handler) handleForgeProviderFailure(
	w http.ResponseWriter, ctx context.Context, input ForgeExecuteRequest, cause error,
) {
	if input.Command.ReadOnly() {
		h.releaseForgeClaim(ctx, input.RequestID)
		respond.Error(w, http.StatusBadGateway, errors.New("forge provider execution failed"))
		return
	}
	if err := h.forgeReplayStore.MarkUncertain(ctx, input.RequestID,
		[]byte(truncateForgeError(cause.Error(), 512))); err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("failed to record uncertain forge outcome"))
		return
	}
	respond.Error(w, http.StatusBadGateway,
		errors.New("forge provider execution outcome is uncertain and requires reconciliation"))
}

func (h *Handler) completeForgeExecution(
	w http.ResponseWriter, ctx context.Context, input ForgeExecuteRequest, result forgeexec.Response,
) {
	body, err := json.Marshal(result)
	if err == nil {
		err = h.forgeReplayStore.Complete(ctx, input.RequestID, http.StatusOK, body)
	}
	if err == nil {
		writeForgeJSONBytes(w, http.StatusOK, body)
		return
	}
	if input.Command.ReadOnly() {
		h.releaseForgeClaim(ctx, input.RequestID)
	} else {
		_ = h.forgeReplayStore.MarkUncertain(ctx, input.RequestID,
			[]byte(truncateForgeError("provider may have succeeded; response persistence failed", 512)))
	}
	respond.Error(w, http.StatusInternalServerError, errors.New("failed to persist forge execution outcome"))
}

func truncateForgeError(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
