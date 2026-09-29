package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ai-dev-control-plane/api/internal/forgeexec"
	"github.com/ai-dev-control-plane/api/internal/forgereplay"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/api/internal/workloadauth"
)

type ForgeReconcileResponse struct {
	Status    string              `json:"status"`
	Retryable bool                `json:"retryable"`
	Response  *forgeexec.Response `json:"response,omitempty"`
	Evidence  forgeexec.Evidence  `json:"evidence"`
	Reason    string              `json:"reason,omitempty"`
}

func (h *Handler) ReconcileForgeWorkload(w http.ResponseWriter, r *http.Request) {
	if h.workloadVerifier == nil || h.forgeReplayStore == nil || h.forgeExecutor == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("hosted forge reconciliation is not configured"))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxForgeExecuteBody+1))
	if err != nil || len(body) > maxForgeExecuteBody {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid forge reconciliation body"))
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
	state, err := h.forgeReplayStore.Inspect(ctx, forgereplay.Request{
		ID: input.RequestID, WorkloadID: r.Header.Get(workloadauth.HeaderWorkload),
		TaskID: input.TaskID, RunID: input.RunID, Operation: operation, Body: body,
	})
	if err != nil {
		switch {
		case errors.Is(err, forgereplay.ErrNotFound):
			respond.Error(w, http.StatusNotFound, errors.New("forge execution request not found"))
		case errors.Is(err, forgereplay.ErrConflict):
			respond.Error(w, http.StatusConflict, errors.New("request_id is bound to a different forge request"))
		case errors.Is(err, forgereplay.ErrInvalidRequest):
			respond.Error(w, http.StatusBadRequest, err)
		default:
			respond.Error(w, http.StatusInternalServerError, errors.New("failed to inspect forge request"))
		}
		return
	}

	switch state.State {
	case forgereplay.StateReplay:
		h.writeResolvedForgeReconciliation(w, state)
		return
	case forgereplay.StateInFlight:
		respond.Error(w, http.StatusConflict, errors.New("forge request is still in flight or awaiting retry"))
		return
	case forgereplay.StateUncertain:
		// Continue with read-only provider reconciliation.
	default:
		respond.Error(w, http.StatusConflict, errors.New("forge request is not reconcilable"))
		return
	}

	fctx, err := h.loadForgeContext(ctx, input.TaskID, input.RunID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("forge task/run context not found"))
		} else {
			respond.Error(w, http.StatusInternalServerError, errors.New("failed to load forge task/run context"))
		}
		return
	}
	if input.Command.Repository.Owner != fctx.Repository.Owner ||
		input.Command.Repository.Name != fctx.Repository.Name {
		respond.Error(w, http.StatusConflict,
			errors.New("forge command repository does not match canonical task repository"))
		return
	}

	result, err := h.forgeExecutor.Reconcile(ctx, input.Command)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, errors.New("forge provider reconciliation failed"))
		return
	}
	evidenceBody, err := json.Marshal(result.Evidence)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("failed to encode reconciliation evidence"))
		return
	}

	switch result.Status {
	case forgeexec.ReconcileApplied:
		if result.Response == nil {
			respond.Error(w, http.StatusInternalServerError, errors.New("applied reconciliation is missing provider response"))
			return
		}
		responseBody, err := json.Marshal(result.Response)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, errors.New("failed to encode reconciled provider response"))
			return
		}
		if err := h.forgeReplayStore.ResolveUncertain(
			ctx, input.RequestID, http.StatusOK, responseBody, evidenceBody,
		); err != nil {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist reconciled forge outcome: %w", err))
			return
		}
		respond.JSON(w, http.StatusOK, ForgeReconcileResponse{
			Status: string(result.Status), Response: result.Response,
			Evidence: result.Evidence, Reason: result.Reason,
		})
	case forgeexec.ReconcileNotApplied:
		if err := h.forgeReplayStore.RetryUncertain(ctx, input.RequestID, evidenceBody); err != nil {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("make forge request retryable: %w", err))
			return
		}
		respond.JSON(w, http.StatusOK, ForgeReconcileResponse{
			Status: string(result.Status), Retryable: true,
			Evidence: result.Evidence, Reason: result.Reason,
		})
	case forgeexec.ReconcileAmbiguous:
		if err := h.forgeReplayStore.KeepUncertain(ctx, input.RequestID, evidenceBody); err != nil {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist ambiguous forge reconciliation: %w", err))
			return
		}
		respond.JSON(w, http.StatusConflict, ForgeReconcileResponse{
			Status: string(result.Status), Retryable: false,
			Evidence: result.Evidence, Reason: result.Reason,
		})
	default:
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("unknown forge reconciliation status %q", result.Status))
	}
}

func (h *Handler) writeResolvedForgeReconciliation(w http.ResponseWriter, state forgereplay.Result) {
	var response forgeexec.Response
	if err := json.Unmarshal(state.ResponseBody, &response); err != nil {
		respond.Error(w, http.StatusInternalServerError, errors.New("cached forge response is invalid"))
		return
	}
	var evidence forgeexec.Evidence
	if len(state.Evidence) > 0 {
		if err := json.Unmarshal(state.Evidence, &evidence); err != nil {
			respond.Error(w, http.StatusInternalServerError, errors.New("cached forge evidence is invalid"))
			return
		}
	}
	respond.JSON(w, http.StatusOK, ForgeReconcileResponse{
		Status:   string(forgeexec.ReconcileApplied),
		Response: &response,
		Evidence: evidence,
	})
}
