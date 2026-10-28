package verifier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type wpFixture struct {
	server   *httptest.Server
	requests []string
	status   int
	pipeline map[string]any
	files    []map[string]any
}

func newWoodpeckerFixture(t *testing.T) *wpFixture {
	t.Helper()
	f := &wpFixture{}
	f.pipeline = map[string]any{
		"id": 81, "number": 23, "commit": strings.Repeat("a", 40),
		"rerun_count": 0, "timestamp": 1600000000, "status": "success",
		"workflows": []any{map[string]any{"id": 9, "agent_id": 3,
			"started": 1600000005, "finished": 1600000020,
			"children": []any{map[string]any{"id": 7, "name": "test", "state": "success",
				"exit_code": 0, "started": 1600000006, "finished": 1600000018}},
		}},
	}
	f.files = []map[string]any{{"name": ".woodpecker.yml", "data": base64.StdEncoding.EncodeToString([]byte("steps:\n  test: go test ./...\n"))}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer token-read-only" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/repos/11":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 11, "full_name": "dporkka/dev-plane"})
		case "/api/repos/11/pipelines/23":
			_ = json.NewEncoder(w).Encode(f.pipeline)
		case "/api/repos/11/pipelines/23/config":
			_ = json.NewEncoder(w).Encode(f.files)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func wpReader(f *wpFixture) WoodpeckerReader {
	return WoodpeckerReader{BaseURL: f.server.URL, Token: "token-read-only", RepositoryID: 11, Repository: "dporkka/dev-plane"}
}
func wpQuery() RemoteCIQuery {
	return RemoteCIQuery{HeadSHA: strings.Repeat("a", 40), PipelineID: "81", RunID: "23", JobID: "7", AttemptID: "0"}
}
func TestWoodpeckerReadOnlyObservationUsesExactAPIPipelineAndConfig(t *testing.T) {
	f := newWoodpeckerFixture(t)
	obs, err := wpReader(f).ObserveAttempt(context.Background(), wpQuery())
	if err != nil {
		t.Fatal(err)
	}
	if obs.HeadSHA != wpQuery().HeadSHA || obs.WorkerID != "3" ||
		obs.JobName != "test" || obs.ConfigurationSHA256 == "" ||
		obs.StepStatus != "success" || obs.PipelineStatus != "success" {
		t.Fatalf("incomplete observed execution: %+v", obs)
	}
	want := []string{"GET /api/repos/11", "GET /api/repos/11/pipelines/23", "GET /api/repos/11/pipelines/23/config"}
	if fmt.Sprint(f.requests) != fmt.Sprint(want) {
		t.Fatalf("unexpected HTTP calls %v, want %v", f.requests, want)
	}
}

func TestWoodpeckerReaderDoesNotSignInsufficientExecutionProof(t *testing.T) {
	f := newWoodpeckerFixture(t)
	r, err := wpReader(f).FetchAttempt(context.Background(), wpQuery())
	if !errors.Is(err, ErrWoodpeckerAssignmentUnproven) || r.HeadSHA != "" {
		t.Fatalf("must fail closed without authoritative assignment timestamp: %+v err=%v", r, err)
	}
}

func TestWoodpeckerObservationRejectsMismatchedIdentities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*wpFixture, *RemoteCIQuery)
	}{
		{"commit", func(f *wpFixture, _ *RemoteCIQuery) { f.pipeline["commit"] = strings.Repeat("b", 40) }},
		{"rerun", func(f *wpFixture, _ *RemoteCIQuery) { f.pipeline["rerun_count"] = 1 }},
		{"pipeline ID", func(f *wpFixture, _ *RemoteCIQuery) { f.pipeline["id"] = 99 }},
		{"unknown job", func(_ *wpFixture, q *RemoteCIQuery) { q.JobID = "999" }},
		{"bad repo", func(_ *wpFixture, q *RemoteCIQuery) { q.RunID = "24" }},
		{"non-numeric IDs", func(_ *wpFixture, q *RemoteCIQuery) { q.PipelineID = "abc" }},
		{"missing config", func(f *wpFixture, _ *RemoteCIQuery) { f.files = nil }},
		{"duplicate config", func(f *wpFixture, _ *RemoteCIQuery) { f.files = append(f.files, f.files[0]) }},
		{"invalid config bytes", func(f *wpFixture, _ *RemoteCIQuery) { f.files[0]["data"] = "%%bad%%" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWoodpeckerFixture(t)
			q := wpQuery()
			tc.mutate(f, &q)
			if _, err := wpReader(f).ObserveAttempt(context.Background(), q); err == nil {
				t.Fatal("accepted untrusted or mismatched CI data")
			}
		})
	}
}

func TestWoodpeckerReaderRejectsMissingTokenAndUnsafeURLBeforeNetwork(t *testing.T) {
	f := newWoodpeckerFixture(t)
	r := wpReader(f)
	r.Token = ""
	if _, err := r.ObserveAttempt(context.Background(), wpQuery()); err == nil {
		t.Fatal("accepted missing token")
	}
	r = wpReader(f)
	r.BaseURL = "http://ci.example.org"
	if _, err := r.ObserveAttempt(context.Background(), wpQuery()); err == nil {
		t.Fatal("allowed non-local cleartext CI API")
	}
	r = wpReader(f)
	r.BaseURL = f.server.URL + "/redirect?x=1"
	if _, err := r.ObserveAttempt(context.Background(), wpQuery()); err == nil {
		t.Fatal("allowed ambiguous API URL")
	}
	if len(f.requests) != 0 {
		t.Fatalf("unsafe calls reached server: %v", f.requests)
	}
}

func TestWoodpeckerReaderRejectsProviderError(t *testing.T) {
	f := newWoodpeckerFixture(t)
	f.status = 503
	if _, err := wpReader(f).ObserveAttempt(context.Background(), wpQuery()); err == nil {
		t.Fatal("provider outage marked success")
	}
}
