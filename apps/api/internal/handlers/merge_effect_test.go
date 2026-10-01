package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/capability"
	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/policies"
)

type effectMergeGateway struct {
	result   *gateway.MergePRResult
	err      error
	calls    []gateway.MergePRRequest
	onMerge  func()
	remote   *gateway.GitHubPR
	getErr   error
	getCalls int
}

func (f *effectMergeGateway) GetPR(
	ctx context.Context,
	token *oauth2.Token,
	owner, name string,
	number int,
) (*gateway.GitHubPR, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.remote, nil
}

func (f *effectMergeGateway) MergePR(
	ctx context.Context,
	token *oauth2.Token,
	owner, name string,
	number int,
	req gateway.MergePRRequest,
) (*gateway.MergePRResult, error) {
	if f.onMerge != nil {
		f.onMerge()
	}
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestExecuteMergeEffectPersistsIntentBeforeGitHubAndReceiptAfter(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())

	input := mergeEffectInput{
		RunID:         "run-1",
		TaskID:        "task-1",
		PullRequestID: "pr-1",
		Owner:         "dporkka",
		RepoName:      "dev-plane",
		Number:        123,
		Method:        "squash",
		Revision:      "head-abc",
	}
	remote := remoteMergePR("open", false, "head-abc", "")
	intent, err := newMergeEffectIntent(input)
	if err != nil {
		t.Fatalf("newMergeEffectIntent() error = %v", err)
	}

	fake := &effectMergeGateway{
		result: &gateway.MergePRResult{Merged: true, SHA: "merge-sha-1"},
	}
	fake.onMerge = func() {
		record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
		if err != nil {
			t.Fatalf("effect intent was not durable before MergePR: %v", err)
		}
		if record.Receipt != nil {
			t.Fatalf("receipt existed before external merge: %#v", record.Receipt)
		}
	}

	mergeSHA, err := h.executeMergeEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
		remote,
	)
	if err != nil {
		t.Fatalf("executeMergeEffect() error = %v", err)
	}
	if mergeSHA != "merge-sha-1" {
		t.Fatalf("merge SHA = %q, want merge-sha-1", mergeSHA)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("MergePR calls = %d, want 1", len(fake.calls))
	}
	if fake.calls[0].SHA != "head-abc" || fake.calls[0].Method != "squash" {
		t.Fatalf("MergePR request = %#v", fake.calls[0])
	}

	record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Receipt == nil {
		t.Fatal("effect receipt = nil")
	}
	if record.Receipt.Provider != "github" || record.Receipt.Reference != "merge-sha-1" {
		t.Fatalf("receipt = %#v", record.Receipt)
	}
}

func TestExecuteMergeEffectReconcilesRemoteMergeWithoutRepeatingSideEffect(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := mergeEffectInput{
		RunID:         "run-1",
		TaskID:        "task-1",
		PullRequestID: "pr-1",
		Owner:         "dporkka",
		RepoName:      "dev-plane",
		Number:        123,
		Method:        "merge",
		Revision:      "head-abc",
	}
	intent, err := newMergeEffectIntent(input)
	if err != nil {
		t.Fatalf("newMergeEffectIntent() error = %v", err)
	}
	if err := dbpkg.EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent() error = %v", err)
	}

	fake := &effectMergeGateway{
		result: &gateway.MergePRResult{Merged: true, SHA: "must-not-run"},
	}
	mergeSHA, err := h.executeMergeEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
		remoteMergePR("closed", true, "head-abc", "merge-sha-recovered"),
	)
	if err != nil {
		t.Fatalf("executeMergeEffect() recovery error = %v", err)
	}
	if mergeSHA != "merge-sha-recovered" {
		t.Fatalf("merge SHA = %q, want merge-sha-recovered", mergeSHA)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("MergePR calls = %d, want 0 during remote reconciliation", len(fake.calls))
	}

	record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Receipt == nil || record.Receipt.Reference != "merge-sha-recovered" {
		t.Fatalf("recovered receipt = %#v", record.Receipt)
	}
}

func TestExecuteMergeEffectReplaysRecordedReceiptWithoutRepeatingSideEffect(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := mergeEffectInput{
		RunID:         "run-1",
		TaskID:        "task-1",
		PullRequestID: "pr-1",
		Owner:         "dporkka",
		RepoName:      "dev-plane",
		Number:        123,
		Method:        "merge",
		Revision:      "head-abc",
	}
	intent, err := newMergeEffectIntent(input)
	if err != nil {
		t.Fatalf("newMergeEffectIntent() error = %v", err)
	}
	if err := dbpkg.EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("EnsureEffectIntent() error = %v", err)
	}
	receipt, err := newMergeEffectReceipt(intent, "merge-sha-recorded")
	if err != nil {
		t.Fatalf("newMergeEffectReceipt() error = %v", err)
	}
	if err := dbpkg.RecordEffectReceipt(context.Background(), database.DB, receipt); err != nil {
		t.Fatalf("RecordEffectReceipt() error = %v", err)
	}

	fake := &effectMergeGateway{
		result: &gateway.MergePRResult{Merged: true, SHA: "must-not-run"},
	}
	mergeSHA, err := h.executeMergeEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
		remoteMergePR("closed", true, "head-abc", "merge-sha-recorded"),
	)
	if err != nil {
		t.Fatalf("executeMergeEffect() replay error = %v", err)
	}
	if mergeSHA != "merge-sha-recorded" {
		t.Fatalf("merge SHA = %q, want merge-sha-recorded", mergeSHA)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("MergePR calls = %d, want 0 on receipt replay", len(fake.calls))
	}
}

func TestExecuteMergeEffectRejectsAuthorizedHeadDrift(t *testing.T) {
	database := openMergeEffectTestDB(t)
	h := NewHandler(database.DB, slog.Default())
	input := mergeEffectInput{
		RunID:         "run-1",
		TaskID:        "task-1",
		PullRequestID: "pr-1",
		Owner:         "dporkka",
		RepoName:      "dev-plane",
		Number:        123,
		Method:        "merge",
		Revision:      "head-authorized",
	}
	fake := &effectMergeGateway{
		result: &gateway.MergePRResult{Merged: true, SHA: "must-not-run"},
	}

	_, err := h.executeMergeEffect(
		context.Background(),
		fake,
		&oauth2.Token{AccessToken: "token"},
		input,
		remoteMergePR("open", false, "head-drifted", ""),
	)
	if !errors.Is(err, ErrMergeEffectConflict) {
		t.Fatalf("executeMergeEffect() drift error = %v, want %v", err, ErrMergeEffectConflict)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("MergePR calls = %d, want 0 on head drift", len(fake.calls))
	}
}

func remoteMergePR(state string, merged bool, headSHA, mergeSHA string) *gateway.GitHubPR {
	pr := &gateway.GitHubPR{
		State:          state,
		Merged:         merged,
		MergeCommitSHA: mergeSHA,
	}
	pr.Head.SHA = headSHA
	return pr
}

func openMergeEffectTestDB(t *testing.T) *dbpkg.DB {
	t.Helper()
	database, err := dbpkg.New(":memory:")
	if err != nil {
		t.Fatalf("db.New(:memory:) error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`
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
		)
	`)
	if err != nil {
		t.Fatalf("create execution_effects: %v", err)
	}
	return database
}

func TestMergePullRequestRecoversRemoteMergedEffectWithoutCallingMerge(t *testing.T) {
	database := openMergeHandlerTestDB(t)
	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
	fake := &effectMergeGateway{
		remote: remoteMergePR("closed", true, "head-abc", "merge-sha-recovered"),
		result: &gateway.MergePRResult{Merged: true, SHA: "must-not-run"},
	}
	h := NewHandler(database.DB, slog.Default()).
		WithCapabilityKernel(capability.NewKernel(allowAll, nil, nil, slog.Default())).
		WithGitHubGateway(fake).
		WithGitHubToken("gh-token")

	now := time.Now().UTC()
	_, err := database.Exec(`
		INSERT INTO projects (id, organization_id, deleted_at)
		VALUES ('project-1', 'org-1', NULL);
		INSERT INTO repositories (id, project_id, owner, name, deleted_at)
		VALUES ('repo-1', 'project-1', 'dporkka', 'dev-plane', NULL);
		INSERT INTO tasks (id, status, completed_at, updated_at)
		VALUES ('task-1', 'pr_created', NULL, ?);
		INSERT INTO pull_requests (
			id, task_id, run_id, repository_id, number, title, body, branch,
			base_branch, url, state, draft, created_by, merged_at, created_at, updated_at
		) VALUES (
			'pr-1', 'task-1', 'run-1', 'repo-1', 123, 'title', 'body', 'feature',
			'main', 'https://github.com/dporkka/dev-plane/pull/123', 'open', false,
			'user-1', NULL, ?, ?
		)
	`, now, now, now)
	if err != nil {
		t.Fatalf("insert merge handler fixtures: %v", err)
	}

	input := mergeEffectInput{
		RunID:         "run-1",
		TaskID:        "task-1",
		PullRequestID: "pr-1",
		Owner:         "dporkka",
		RepoName:      "dev-plane",
		Number:        123,
		Method:        "merge",
		Revision:      "head-abc",
	}
	intent, err := newMergeEffectIntent(input)
	if err != nil {
		t.Fatalf("newMergeEffectIntent() error = %v", err)
	}
	if err := dbpkg.EnsureEffectIntent(context.Background(), database.DB, intent); err != nil {
		t.Fatalf("seed crash-window intent: %v", err)
	}

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest("pr-1", `{"merge_method":"merge","sha":"head-abc"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if fake.getCalls != 1 {
		t.Fatalf("GetPR calls = %d, want 1", fake.getCalls)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("MergePR calls = %d, want 0 during crash recovery", len(fake.calls))
	}

	var prState, taskStatus string
	if err := database.QueryRow(`SELECT state FROM pull_requests WHERE id = 'pr-1'`).Scan(&prState); err != nil {
		t.Fatalf("load pull request state: %v", err)
	}
	if err := database.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("load task status: %v", err)
	}
	if prState != "merged" || taskStatus != "done" {
		t.Fatalf("local state = pr:%q task:%q, want merged/done", prState, taskStatus)
	}

	record, err := dbpkg.LoadEffect(context.Background(), database.DB, intent.ID)
	if err != nil {
		t.Fatalf("LoadEffect() error = %v", err)
	}
	if record.Receipt == nil || record.Receipt.Reference != "merge-sha-recovered" {
		t.Fatalf("receipt = %#v", record.Receipt)
	}
}

func openMergeHandlerTestDB(t *testing.T) *dbpkg.DB {
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
			completed_at TIMESTAMP,
			updated_at TIMESTAMP
		);
		CREATE TABLE pull_requests (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			run_id TEXT,
			repository_id TEXT NOT NULL,
			number INTEGER NOT NULL,
			title TEXT NOT NULL,
			body TEXT,
			branch TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			url TEXT NOT NULL,
			state TEXT NOT NULL,
			draft BOOLEAN NOT NULL,
			created_by TEXT NOT NULL,
			merged_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
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
		)
	`)
	if err != nil {
		t.Fatalf("create merge handler schema: %v", err)
	}
	return database
}
