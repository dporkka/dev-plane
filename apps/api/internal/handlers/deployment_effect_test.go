package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"golang.org/x/oauth2"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/gateway"
)

type effectDeployGateway struct {
	resolvedSHA string
	resolveErr  error
	list        []gateway.Deployment
	listErr     error
	created     *gateway.Deployment
	createErr   error

	resolveCalls int
	listCalls    int
	createCalls  int
	createRef    string
	createEnv    string
	createPayload map[string]any
	onCreate     func()
}

func (f *effectDeployGateway) ResolveCommitSHA(
	ctx context.Context,
	token *oauth2.Token,
	owner, name, ref string,
) (string, error) {
	f.resolveCalls++
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return f.resolvedSHA, nil
}

func (f *effectDeployGateway) ListDeployments(
	ctx context.Context,
	token *oauth2.Token,
	owner, name string,
	opts gateway.DeploymentListOptions,
) ([]gateway.Deployment, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]gateway.Deployment(nil), f.list...), nil
}

func (f *effectDeployGateway) CreateDeploymentWithPayload(
	ctx context.Context,
	token *oauth2.Token,
	owner, name, environment, ref string,
	payload map[string]any,
) (*gateway.Deployment, error) {
	f.createCalls++
	f.createRef = ref
	f.createEnv = environment
	f.createPayload = payload
	if f.onCreate != nil {
		f.onCreate()
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.created, nil
}

func TestExecuteDeploymentEffectPersistsIntentBeforeCreateAndCorrelatesPayload(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := deploymentEffectInput{
		RunID:      "run-1",
		TaskID:     "task-1",
		ProjectID:  "project-1",
		RepoID:     "repo-1",
		Owner:      "dporkka",
		RepoName:   "dev-plane",
		Environment:"staging",
		CommitSHA:  "commit-abc",
	}
	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		t.Fatalf("newDeploymentEffectIntent() error = %v", err)
	}

	fake := &effectDeployGateway{
		created: &gateway.Deployment{
			ID:          12345,
			URL:         "https://api.github.com/repos/dporkka/dev-plane/deployments/12345",
			SHA:         "commit-abc",
			Ref:         "commit-abc",
			Environment: "staging",
		},
	}
	fake.onCreate = func() {
		record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
		if err != nil {
			t.Fatalf("effect intent was not durable before deployment create: %v", err)
		}
		if record.Receipt != nil {
			t.Fatalf("receipt existed before external deployment: %#v", record.Receipt)
		}
	}

	deployment, err := h.executeDeploymentEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
	)
	if err != nil {
		t.Fatalf("executeDeploymentEffect() error = %v", err)
	}
	if deployment.ID != 12345 {
		t.Fatalf("deployment ID = %d, want 12345", deployment.ID)
	}
	if fake.createCalls != 1 || fake.listCalls != 1 {
		t.Fatalf("calls = list:%d create:%d, want 1/1", fake.listCalls, fake.createCalls)
	}
	if fake.createRef != "commit-abc" || fake.createEnv != "staging" {
		t.Fatalf("create target = ref:%q env:%q", fake.createRef, fake.createEnv)
	}
	if got := fake.createPayload["dev_plane_effect_id"]; got != string(intent.ID) {
		t.Fatalf("correlation effect id = %v, want %s", got, intent.ID)
	}

	record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Receipt == nil || record.Receipt.Reference != "12345" {
		t.Fatalf("receipt = %#v", record.Receipt)
	}
}

func TestExecuteDeploymentEffectRecoversRemoteCorrelatedDeploymentWithoutCreatingAgain(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := deploymentEffectInput{
		RunID:       "run-1",
		TaskID:      "task-1",
		ProjectID:   "project-1",
		RepoID:      "repo-1",
		Owner:       "dporkka",
		RepoName:    "dev-plane",
		Environment: "production",
		CommitSHA:   "commit-abc",
	}
	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		t.Fatalf("newDeploymentEffectIntent() error = %v", err)
	}
	if err := dbpkg.EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent() error = %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"dev_plane_effect_id": string(intent.ID)})
	fake := &effectDeployGateway{
		list: []gateway.Deployment{{
			ID:          98765,
			URL:         "https://api.github.com/repos/dporkka/dev-plane/deployments/98765",
			SHA:         "commit-abc",
			Ref:         "commit-abc",
			Environment: "production",
			Payload:     payload,
		}},
		created: &gateway.Deployment{ID: 11111},
	}

	deployment, err := h.executeDeploymentEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
	)
	if err != nil {
		t.Fatalf("executeDeploymentEffect() recovery error = %v", err)
	}
	if deployment.ID != 98765 {
		t.Fatalf("deployment ID = %d, want recovered 98765", deployment.ID)
	}
	if fake.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", fake.createCalls)
	}

	record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Receipt == nil || record.Receipt.Reference != "98765" {
		t.Fatalf("recovered receipt = %#v", record.Receipt)
	}
}

func TestExecuteDeploymentEffectReplaysReceiptWithoutCreatingAgain(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := deploymentEffectInput{
		RunID:       "run-1",
		TaskID:      "task-1",
		ProjectID:   "project-1",
		RepoID:      "repo-1",
		Owner:       "dporkka",
		RepoName:    "dev-plane",
		Environment: "production",
		CommitSHA:   "commit-abc",
	}
	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		t.Fatalf("newDeploymentEffectIntent() error = %v", err)
	}
	if err := dbpkg.EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent() error = %v", err)
	}
	remote := gateway.Deployment{
		ID:          98765,
		URL:         "https://api.github.com/repos/dporkka/dev-plane/deployments/98765",
		SHA:         "commit-abc",
		Ref:         "commit-abc",
		Environment: "production",
	}
	payload, _ := json.Marshal(map[string]any{"dev_plane_effect_id": string(intent.ID)})
	remote.Payload = payload
	receipt, err := newDeploymentEffectReceipt(intent, &remote)
	if err != nil {
		t.Fatalf("newDeploymentEffectReceipt() error = %v", err)
	}
	if err := dbpkg.RecordEffectReceipt(context.Background(), database.DB, receipt); err != nil {
		t.Fatalf("RecordEffectReceipt() error = %v", err)
	}
	fake := &effectDeployGateway{list: []gateway.Deployment{remote}}

	deployment, err := h.executeDeploymentEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
	)
	if err != nil {
		t.Fatalf("executeDeploymentEffect() replay error = %v", err)
	}
	if deployment.ID != 98765 || fake.createCalls != 0 {
		t.Fatalf("replay deployment=%#v createCalls=%d", deployment, fake.createCalls)
	}
}

func TestExecuteDeploymentEffectRejectsAmbiguousRemoteCorrelation(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := deploymentEffectInput{
		RunID:       "run-1",
		TaskID:      "task-1",
		ProjectID:   "project-1",
		RepoID:      "repo-1",
		Owner:       "dporkka",
		RepoName:    "dev-plane",
		Environment: "staging",
		CommitSHA:   "commit-abc",
	}
	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		t.Fatalf("newDeploymentEffectIntent() error = %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"dev_plane_effect_id": string(intent.ID)})
	fake := &effectDeployGateway{
		list: []gateway.Deployment{
			{ID: 1, SHA: "commit-abc", Environment: "staging", Payload: payload},
			{ID: 2, SHA: "commit-abc", Environment: "staging", Payload: payload},
		},
	}

	_, err = h.executeDeploymentEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
	)
	if !errors.Is(err, ErrDeploymentEffectConflict) {
		t.Fatalf("executeDeploymentEffect() error = %v, want %v", err, ErrDeploymentEffectConflict)
	}
	if fake.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", fake.createCalls)
	}
}
