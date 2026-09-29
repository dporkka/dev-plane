package forgeexec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type seenRequest struct {
	Method string
	Path   string
	Auth   string
	Body   map[string]any
}

func testGitea(t *testing.T, responseStatus int, responseBody string) (*GiteaExecutor, *[]seenRequest, func()) {
	t.Helper()
	var mu sync.Mutex
	seen := []seenRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Lock()
		seen = append(seen, seenRequest{
			Method: r.Method,
			Path:   r.URL.RequestURI(),
			Auth:   r.Header.Get("Authorization"),
			Body:   body,
		})
		mu.Unlock()
		w.WriteHeader(responseStatus)
		_, _ = w.Write([]byte(responseBody))
	}))
	return NewGiteaExecutor(server.URL, "token-123", server.Client()), &seen, server.Close
}

func repo() RepositoryRef {
	return RepositoryRef{Owner: "acme", Name: "widgets"}
}

func TestGiteaExecutorCreateBranchUsesServerHeldCredential(t *testing.T) {
	exec, seen, cleanup := testGitea(t, 201, `{"name":"agent/task-1","commit":{"id":"deadbeef"}}`)
	defer cleanup()

	response, err := exec.Execute(context.Background(), Command{
		Type:       "create_branch",
		Repository: repo(),
		Name:       "agent/task-1",
		From:       "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != "branch" {
		t.Fatalf("response type = %q", response.Type)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d", len(*seen))
	}
	got := (*seen)[0]
	if got.Method != http.MethodPost || got.Path != "/api/v1/repos/acme/widgets/branches" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	if got.Auth != "token token-123" {
		t.Fatalf("authorization = %q", got.Auth)
	}
}

func TestGiteaExecutorWriteFilePreservesExpectedSHA(t *testing.T) {
	exec, seen, cleanup := testGitea(t, 200, `{"commit":{"sha":"cafebabe"}}`)
	defer cleanup()

	response, err := exec.Execute(context.Background(), Command{
		Type:            "write_file",
		Repository:      repo(),
		Path:            "src/lib.rs",
		Branch:          "agent/task-1",
		Content:         []int{104, 101, 108, 108, 111},
		Message:         "agent: update",
		ExpectedBlobSHA: strptr("oldsha"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != "commit" {
		t.Fatalf("response type = %q", response.Type)
	}
	got := (*seen)[0]
	if got.Method != http.MethodPut || got.Path != "/api/v1/repos/acme/widgets/contents/src/lib.rs" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	if got.Body["sha"] != "oldsha" || got.Body["content"] != "aGVsbG8=" {
		t.Fatalf("body = %#v", got.Body)
	}
}

func TestGiteaExecutorMergePreservesExpectedHead(t *testing.T) {
	exec, seen, cleanup := testGitea(t, 200, "")
	defer cleanup()

	response, err := exec.Execute(context.Background(), Command{
		Type:            "merge_change",
		Repository:      repo(),
		Number:          17,
		Method:          "squash",
		ExpectedHeadSHA: strptr("deadbeef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != "merge" {
		t.Fatalf("response type = %q", response.Type)
	}
	got := (*seen)[0]
	if got.Path != "/api/v1/repos/acme/widgets/pulls/17/merge" {
		t.Fatalf("path = %q", got.Path)
	}
	if got.Body["head_commit_id"] != "deadbeef" || got.Body["do"] != "squash" {
		t.Fatalf("body = %#v", got.Body)
	}
}

func TestGiteaExecutorRejectsUnsafeFilePathBeforeHTTP(t *testing.T) {
	exec, seen, cleanup := testGitea(t, 200, "{}")
	defer cleanup()

	_, err := exec.Execute(context.Background(), Command{
		Type:       "read_file",
		Repository: repo(),
		Path:       "../secrets",
	})
	if err == nil {
		t.Fatal("expected unsafe path error")
	}
	if len(*seen) != 0 {
		t.Fatalf("requests = %d, want 0", len(*seen))
	}
}

func TestCommandOperationIsDerivedFromType(t *testing.T) {
	tests := map[string]string{
		"read_file":     "repo.read",
		"create_branch": "branch.create",
		"write_file":    "commit.write",
		"create_change": "change.create",
		"review_change": "change.review",
		"merge_change":  "change.merge",
		"list_checks":   "check.read",
	}
	for typ, want := range tests {
		got, err := (Command{Type: typ}).Operation()
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if got != want {
			t.Fatalf("%s: operation = %q, want %q", typ, got, want)
		}
	}
	if _, err := (Command{Type: "superuser"}).Operation(); err == nil {
		t.Fatal("unknown command type should fail closed")
	}
}

func strptr(v string) *string { return &v }
