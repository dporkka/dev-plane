package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/capability"
	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
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
	listOptions   gateway.DeploymentListOptions
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
	f.listOptions = opts
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


func TestDeployTaskRecoversStoredCommitWhenMutableRefMoved(t *testing.T) {
	database := openDeploymentHandlerTestDB(t)
	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})

	input := deploymentEffectInput{
		RunID:       "task-1",
		TaskID:      "task-1",
		ProjectID:   "project-1",
		RepoID:      "repo-1",
		Owner:       "dporkka",
		RepoName:    "dev-plane",
		Environment: "staging",
		CommitSHA:   "commit-old",
	}
	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		t.Fatalf("newDeploymentEffectIntent() error = %v", err)
	}
	if err := dbpkg.EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("seed deployment intent: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"dev_plane_effect_id": string(intent.ID)})

	fake := &effectDeployGateway{
		resolvedSHA: "commit-new",
		list: []gateway.Deployment{{
			ID:          98765,
			URL:         "https://api.github.com/repos/dporkka/dev-plane/deployments/98765",
			SHA:         "commit-old",
			Ref:         "commit-old",
			Environment: "staging",
			Payload:     payload,
		}},
	}
	h := NewHandler(database.DB, slog.Default()).
		WithCapabilityKernel(capability.NewKernel(allowAll, nil, nil, slog.Default())).
		WithDeployGateway(fake).
		WithDeployToken("gh-token")

	body, _ := json.Marshal(DeployTaskRequest{Environment: "staging", Ref: "main"})
	req := httptest.NewRequest(http.MethodPost, "/tasks/task-1/deploy", bytes.NewReader(body))
	req = req.WithContext(withRole(req.Context(), models.RoleAdmin))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "task-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.DeployTask(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if fake.resolveCalls != 0 {
		t.Fatalf("ResolveCommitSHA calls = %d, want 0 for persisted intent", fake.resolveCalls)
	}
	if fake.createCalls != 0 {
		t.Fatalf("CreateDeployment calls = %d, want 0 during recovery", fake.createCalls)
	}
	if fake.listOptions.SHA != "commit-old" || fake.listOptions.Environment != "staging" {
		t.Fatalf("reconciliation options = %#v", fake.listOptions)
	}

	var externalID, ref, taskStatus string
	if err := database.QueryRow(`
		SELECT external_id, ref FROM deployments WHERE task_id = 'task-1'
	`).Scan(&externalID, &ref); err != nil {
		t.Fatalf("load local deployment: %v", err)
	}
	if err := database.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("load task status: %v", err)
	}
	if externalID != "98765" || ref != "commit-old" || taskStatus != "deploying" {
		t.Fatalf("local recovery = external:%q ref:%q task:%q", externalID, ref, taskStatus)
	}
}

func openDeploymentHandlerTestDB(t *testing.T) *dbpkg.DB {
	t.Helper()
	database, err := dbpkg.New(":memory:")
	if err != nil {
		t.Fatalf("db.New(:memory:) error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			deleted_at TIMESTAMP
		);
		CREATE TABLE repositories (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			owner TEXT NOT NULL,
			name TEXT NOT NULL,
			deleted_at TIMESTAMP
		);
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			target_branch TEXT,
			project_id TEXT NOT NULL,
			completed_at TIMESTAMP,
			updated_at TIMESTAMP,
			deleted_at TIMESTAMP
		);
		CREATE TABLE deployments (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			environment TEXT NOT NULL,
			ref TEXT NOT NULL,
			provider TEXT NOT NULL,
			external_id TEXT,
			status TEXT NOT NULL,
			url TEXT,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE UNIQUE INDEX idx_deployments_provider_external_id
			ON deployments(provider, external_id)
			WHERE external_id IS NOT NULL;
		CREATE TABLE execution_effects (
			effect_id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			activation_id TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			ordinal INTEGER NOT NULL,
			operation TEXT NOT NULL,
			resource TEXT NOT NULL,
			revision TEXT NOT NULL DEFAULT '',
			input_digest TEXT NOT NULL,
			provider TEXT,
			reference TEXT,
			output_digest TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP
		);

		INSERT INTO projects (id, organization_id, deleted_at)
		VALUES ('project-1', 'org-1', NULL);
		INSERT INTO repositories (id, project_id, owner, name, deleted_at)
		VALUES ('repo-1', 'project-1', 'dporkka', 'dev-plane', NULL);
		INSERT INTO tasks (
			id, status, repository_id, target_branch, project_id, updated_at, deleted_at
		) VALUES (
			'task-1', 'pr_created', 'repo-1', 'main', 'project-1', CURRENT_TIMESTAMP, NULL
		);
	`)
	if err != nil {
		t.Fatalf("create deployment handler schema: %v", err)
	}
	return database
}

