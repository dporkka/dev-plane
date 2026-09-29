package forgeexec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type githubSeenRequest struct {
	Method     string
	Path       string
	Auth       string
	Accept     string
	APIVersion string
	Body       map[string]any
}

func testGitHub(t *testing.T, handler http.HandlerFunc) (*GitHubExecutor, *[]githubSeenRequest, func()) {
	t.Helper()
	var mu sync.Mutex
	seen := []githubSeenRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Lock()
		seen = append(seen, githubSeenRequest{
			Method: r.Method,
			Path: r.URL.RequestURI(),
			Auth: r.Header.Get("Authorization"),
			Accept: r.Header.Get("Accept"),
			APIVersion: r.Header.Get("X-GitHub-Api-Version"),
			Body: body,
		})
		mu.Unlock()
		handler(w, r)
	}))
	return NewGitHubExecutor(server.URL, "token-123", server.Client()), &seen, server.Close
}

func TestGitHubExecutorCreateBranchResolvesSourceRefAndUsesServerCredential(t *testing.T) {
	exec, seen, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/git/ref/heads/main":
			_, _ = w.Write([]byte(`{"ref":"refs/heads/main","object":{"sha":"deadbeef"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/git/refs":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ref":"refs/heads/agent/task-1","object":{"sha":"deadbeef"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	response, err := exec.Execute(context.Background(), Command{
		Type: "create_branch", Repository: repo(), Name: "agent/task-1", From: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	branch, ok := response.Value.(BranchRef)
	if !ok || branch.Name != "agent/task-1" || branch.CommitID != "deadbeef" {
		t.Fatalf("response = %#v", response)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2", len(*seen))
	}
	for _, got := range *seen {
		if got.Auth != "Bearer token-123" {
			t.Fatalf("authorization = %q", got.Auth)
		}
		if got.Accept != "application/vnd.github+json" {
			t.Fatalf("accept = %q", got.Accept)
		}
		if got.APIVersion != "2026-03-10" {
			t.Fatalf("api version = %q", got.APIVersion)
		}
	}
	if (*seen)[1].Body["ref"] != "refs/heads/agent/task-1" || (*seen)[1].Body["sha"] != "deadbeef" {
		t.Fatalf("create ref body = %#v", (*seen)[1].Body)
	}
}

func TestGitHubExecutorWriteFileUsesContentsAPIAndExpectedBlobSHA(t *testing.T) {
	exec, seen, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/acme/widgets/contents/src/lib.rs" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"content":{"sha":"blobsha"},"commit":{"sha":"cafebabe"}}`))
	})
	defer cleanup()

	response, err := exec.Execute(context.Background(), Command{
		Type: "write_file", Repository: repo(), Path: "src/lib.rs", Branch: "agent/task-1",
		Content: []int{104,101,108,108,111}, Message: "agent: update", ExpectedBlobSHA: strptr("oldsha"),
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, ok := response.Value.(CommitRef)
	if !ok || commit.ID != "cafebabe" {
		t.Fatalf("response = %#v", response)
	}
	got := (*seen)[0]
	if got.Body["sha"] != "oldsha" || got.Body["branch"] != "agent/task-1" || got.Body["content"] != "aGVsbG8=" {
		t.Fatalf("body = %#v", got.Body)
	}
}

func TestGitHubExecutorCreateChangeRetainsProviderHeadSHA(t *testing.T) {
	exec, _, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/widgets/pulls" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.test/acme/widgets/pull/42","state":"open","head":{"ref":"agent/task-1","sha":"deadbeef"},"base":{"ref":"main"}}`))
	})
	defer cleanup()

	response, err := exec.Execute(context.Background(), Command{
		Type:"create_change", Repository:repo(), Head:"agent/task-1", Base:"main", Title:"Ship it", Body:"body",
	})
	if err != nil { t.Fatal(err) }
	change, ok := response.Value.(ChangeRef)
	if !ok || change.Number != 42 || change.HeadSHA != "deadbeef" || change.Head != "agent/task-1" || change.Base != "main" {
		t.Fatalf("response = %#v", response)
	}
}

func TestGitHubExecutorReviewMapsProviderEventAndCommit(t *testing.T) {
	exec, seen, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/widgets/pulls/17/reviews" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":7,"state":"APPROVED","commit_id":"deadbeef"}`))
	})
	defer cleanup()
	commit := "deadbeef"
	response, err := exec.Execute(context.Background(), Command{
		Type:"review_change", Repository:repo(), Number:17, Event:"approve", Body:"looks good", CommitID:&commit,
	})
	if err != nil { t.Fatal(err) }
	review, ok := response.Value.(ReviewRef)
	if !ok || review.ID == nil || *review.ID != 7 {
		t.Fatalf("response = %#v", response)
	}
	got := (*seen)[0]
	if got.Body["event"] != "APPROVE" || got.Body["commit_id"] != "deadbeef" {
		t.Fatalf("body = %#v", got.Body)
	}
}

func TestGitHubExecutorMergeUsesExpectedHeadAndSupportedMethod(t *testing.T) {
	exec, seen, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/acme/widgets/pulls/17/merge" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"sha":"merge-sha","merged":true,"message":"merged"}`))
	})
	defer cleanup()
	response, err := exec.Execute(context.Background(), Command{
		Type:"merge_change", Repository:repo(), Number:17, Method:"squash", ExpectedHeadSHA:strptr("deadbeef"),
	})
	if err != nil { t.Fatal(err) }
	merge, ok := response.Value.(MergeResult)
	if !ok || !merge.Merged { t.Fatalf("response = %#v", response) }
	got:=(*seen)[0]
	if got.Body["sha"]!="deadbeef" || got.Body["merge_method"]!="squash" {
		t.Fatalf("body = %#v", got.Body)
	}
}

func TestGitHubExecutorRejectsUnsupportedMergeMethodBeforeHTTP(t *testing.T) {
	exec, seen, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	defer cleanup()
	_, err := exec.Execute(context.Background(), Command{
		Type:"merge_change", Repository:repo(), Number:17, Method:"fast_forward_only",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported GitHub merge method") {
		t.Fatalf("err = %v", err)
	}
	if len(*seen)!=0 { t.Fatalf("requests=%d",len(*seen)) }
}

func TestGitHubExecutorListChecksMapsCheckRuns(t *testing.T) {
	exec, _, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/commits/deadbeef/check-runs" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"check_runs":[{"id":123,"name":"CI","status":"completed","conclusion":"success","html_url":"https://github.test/check/123"},{"id":124,"name":"Lint","status":"in_progress","conclusion":null,"html_url":"https://github.test/check/124"}]}`))
	})
	defer cleanup()
	ref:="deadbeef"
	response, err:=exec.Execute(context.Background(), Command{Type:"list_checks",Repository:repo(),Reference:&ref})
	if err!=nil {t.Fatal(err)}
	checks,ok:=response.Value.([]CheckRun)
	if !ok || len(checks)!=2 || checks[0].ID!="123" || checks[0].Context!="CI" || checks[0].State!="success" || checks[1].State!="in_progress" {
		t.Fatalf("checks = %#v", response.Value)
	}
}

func TestGitHubExecutorReadFileDecodesContentsAPI(t *testing.T) {
	exec, _, cleanup := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref")!="main" { t.Fatalf("ref=%q",r.URL.Query().Get("ref")) }
		_, _ = w.Write([]byte(`{"type":"file","path":"README.md","sha":"blobsha","encoding":"base64","content":"aGVsbG8="}`))
	})
	defer cleanup()
	ref:="main"
	response,err:=exec.Execute(context.Background(),Command{Type:"read_file",Repository:repo(),Path:"README.md",Reference:&ref})
	if err!=nil {t.Fatal(err)}
	file,ok:=response.Value.(FileContent)
	if !ok || file.SHA!="blobsha" || string([]byte{byte(file.Content[0]),byte(file.Content[1]),byte(file.Content[2]),byte(file.Content[3]),byte(file.Content[4])})!="hello" {
		t.Fatalf("response=%#v",response)
	}
}
