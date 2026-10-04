package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	models "github.com/ai-dev-control-plane/models"
)

type fakeGoalStore struct {
	putGoalFn          func(context.Context, models.Goal) (models.Goal, error)
	getGoalFn          func(context.Context, string) (models.Goal, error)
	linkGoalWorkItemFn func(context.Context, string, string, string) (models.Goal, error)
	listGoalProofsFn   func(context.Context, string) ([]models.GoalProof, error)
}

func (f *fakeGoalStore) PutGoal(ctx context.Context, goal models.Goal) (models.Goal, error) {
	return f.putGoalFn(ctx, goal)
}
func (f *fakeGoalStore) GetGoal(ctx context.Context, id string) (models.Goal, error) {
	return f.getGoalFn(ctx, id)
}
func (f *fakeGoalStore) LinkGoalWorkItem(ctx context.Context, goalID, workItemID, subjectRevision string) (models.Goal, error) {
	return f.linkGoalWorkItemFn(ctx, goalID, workItemID, subjectRevision)
}
func (f *fakeGoalStore) ListGoalProofs(ctx context.Context, goalID string) ([]models.GoalProof, error) {
	return f.listGoalProofsFn(ctx, goalID)
}

func TestCreateGoalBindsAuthenticatedOrganizationAndCreator(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS").WithArgs("user-1", "org-1").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	store := &fakeGoalStore{}
	store.putGoalFn = func(_ context.Context, goal models.Goal) (models.Goal, error) {
		if goal.OrganizationID != "org-1" || goal.CreatedBy != "user-1" {
			t.Fatalf("goal authority = org %q user %q", goal.OrganizationID, goal.CreatedBy)
		}
		if goal.Status != models.GoalStatusActive {
			t.Fatalf("status = %q, want active", goal.Status)
		}
		goal.ProofEpoch = "epoch:assigned"
		return goal, nil
	}
	store.getGoalFn = unexpectedGetGoal(t)
	store.linkGoalWorkItemFn = unexpectedLinkGoal(t)
	store.listGoalProofsFn = unexpectedListProofs(t)

	h := NewHandler(db, slog.Default()).WithGoalStore(store)
	body := `{"title":"Ship demo","objective":"Make the demo reliable","success_criteria":[{"id":"smoke","description":"Smoke passes","required":true}]}`
	req := goalRequest(t, http.MethodPost, "/api/v1/organizations/org-1/goals", body, "orgID", "org-1")
	rr := httptest.NewRecorder()
	h.CreateGoal(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got models.Goal
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ProofEpoch != "epoch:assigned" {
		t.Fatalf("proof_epoch = %q", got.ProofEpoch)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetGoalHidesCrossOrganizationGoal(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &fakeGoalStore{
		getGoalFn: func(context.Context, string) (models.Goal, error) {
			return models.Goal{ID: "goal-1", OrganizationID: "org-2"}, nil
		},
		putGoalFn: unexpectedPutGoal(t), linkGoalWorkItemFn: unexpectedLinkGoal(t), listGoalProofsFn: unexpectedListProofs(t),
	}
	h := NewHandler(db, slog.Default()).WithGoalStore(store)
	req := goalRequest(t, http.MethodGet, "/api/v1/goals/goal-1", "", "id", "goal-1")
	rr := httptest.NewRecorder()
	h.GetGoal(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestEvaluateGoalIsReadOnlyAndUsesStoredProofHistory(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	criterion := models.GoalCriterion{ID: "tests", Description: "Tests pass", Required: true}
	goal := models.Goal{
		ID: "goal-1", OrganizationID: "org-1", CreatedBy: "user-1", Title: "Ship", Objective: "Ship safely",
		Status: models.GoalStatusVerifying, ProofEpoch: "epoch:current", SuccessCriteria: []models.GoalCriterion{criterion},
	}
	proof := models.GoalProof{
		CriterionID: "tests", CriterionDigest: criterion.Digest(), EvidenceID: "evidence-1", SubjectRevision: "git-commit:abc",
		ProofEpoch: goal.ProofEpoch, Status: models.GoalProofPassed, ObservedAt: time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC),
	}
	store := &fakeGoalStore{
		getGoalFn: func(context.Context, string) (models.Goal, error) { return goal, nil },
		listGoalProofsFn: func(context.Context, string) ([]models.GoalProof, error) { return []models.GoalProof{proof}, nil },
		putGoalFn: unexpectedPutGoal(t), linkGoalWorkItemFn: unexpectedLinkGoal(t),
	}
	h := NewHandler(db, slog.Default()).WithGoalStore(store)
	req := goalRequest(t, http.MethodGet, "/api/v1/goals/goal-1/evaluation", "", "id", "goal-1")
	rr := httptest.NewRecorder()
	h.EvaluateGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got GoalEvaluationResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Evaluation.Status != models.GoalEvaluationProven {
		t.Fatalf("evaluation = %#v", got.Evaluation)
	}
	if got.Goal.Status != models.GoalStatusVerifying {
		t.Fatalf("evaluation mutated operational status to %q", got.Goal.Status)
	}
}

func TestLinkGoalWorkItemPassesExactSubjectRevision(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goal := models.Goal{ID: "goal-1", OrganizationID: "org-1", CreatedBy: "user-1"}
	store := &fakeGoalStore{
		getGoalFn: func(context.Context, string) (models.Goal, error) { return goal, nil },
		linkGoalWorkItemFn: func(_ context.Context, goalID, workItemID, subjectRevision string) (models.Goal, error) {
			if goalID != "goal-1" || workItemID != "DEV-9" || subjectRevision != "git-commit:abc" {
				t.Fatalf("link = %q %q %q", goalID, workItemID, subjectRevision)
			}
			goal.ProofEpoch = "epoch:rotated"
			return goal, nil
		},
		putGoalFn: unexpectedPutGoal(t), listGoalProofsFn: unexpectedListProofs(t),
	}
	h := NewHandler(db, slog.Default()).WithGoalStore(store)
	req := goalRequest(t, http.MethodPost, "/api/v1/goals/goal-1/work-items", `{"work_item_id":"DEV-9","subject_revision":"git-commit:abc"}`, "id", "goal-1")
	rr := httptest.NewRecorder()
	h.LinkGoalWorkItem(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func goalRequest(t *testing.T, method, path, body, param, value string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1", OrgID: "org-1", Role: "admin"})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(param, value)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

func unexpectedPutGoal(t *testing.T) func(context.Context, models.Goal) (models.Goal, error) {
	return func(context.Context, models.Goal) (models.Goal, error) { t.Fatal("unexpected PutGoal"); return models.Goal{}, nil }
}
func unexpectedGetGoal(t *testing.T) func(context.Context, string) (models.Goal, error) {
	return func(context.Context, string) (models.Goal, error) { t.Fatal("unexpected GetGoal"); return models.Goal{}, nil }
}
func unexpectedLinkGoal(t *testing.T) func(context.Context, string, string, string) (models.Goal, error) {
	return func(context.Context, string, string, string) (models.Goal, error) { t.Fatal("unexpected LinkGoalWorkItem"); return models.Goal{}, nil }
}
func unexpectedListProofs(t *testing.T) func(context.Context, string) ([]models.GoalProof, error) {
	return func(context.Context, string) ([]models.GoalProof, error) { t.Fatal("unexpected ListGoalProofs"); return nil, nil }
}
