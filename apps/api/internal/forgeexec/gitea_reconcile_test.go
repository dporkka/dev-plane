package forgeexec

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func reconcileServer(t *testing.T, handler http.HandlerFunc) (*GiteaExecutor, func()) {
	t.Helper()
	s := httptest.NewServer(handler)
	return NewGiteaExecutor(s.URL, "token-123", s.Client()), s.Close
}

func TestGiteaReconcileCreateBranchApplied(t *testing.T) {
	exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/acme/widgets/branches/agent/task-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"name":"agent/task-1","commit":{"id":"deadbeef"}}`))
	})
	defer cleanup()

	result, err := exec.Reconcile(context.Background(), Command{
		Type: "create_branch", Repository: repo(), Name: "agent/task-1", From: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconcileApplied || result.Response == nil || result.Response.Type != "branch" {
		t.Fatalf("result = %+v", result)
	}
	if result.Evidence.HeadSHA != "deadbeef" || result.Evidence.Provider != "gitea" {
		t.Fatalf("evidence = %+v", result.Evidence)
	}
}

func TestGiteaReconcileCreateBranchAbsentIsRetryable(t *testing.T) {
	exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	defer cleanup()

	result, err := exec.Reconcile(context.Background(), Command{
		Type: "create_branch", Repository: repo(), Name: "agent/task-1", From: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconcileNotApplied {
		t.Fatalf("status = %q", result.Status)
	}
}

func TestGiteaReconcileWriteFileMatchesContentAndCapturesBranchHead(t *testing.T) {
	exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/contents/src/lib.rs"):
			if r.URL.Query().Get("ref") != "agent/task-1" {
				t.Fatalf("ref = %q", r.URL.Query().Get("ref"))
			}
			_, _ = w.Write([]byte(`{"path":"src/lib.rs","sha":"blobsha","encoding":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte("hello")) + `"}`))
		case strings.Contains(r.URL.Path, "/branches/agent/task-1"):
			_, _ = w.Write([]byte(`{"name":"agent/task-1","commit":{"id":"commitsha"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	result, err := exec.Reconcile(context.Background(), Command{
		Type: "write_file", Repository: repo(), Path: "src/lib.rs", Branch: "agent/task-1",
		Content: []int{104, 101, 108, 108, 111}, Message: "update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconcileApplied {
		t.Fatalf("result = %+v", result)
	}
	if result.Evidence.HeadSHA != "commitsha" {
		t.Fatalf("head = %q", result.Evidence.HeadSHA)
	}
	commit, ok := result.Response.Value.(CommitRef)
	if !ok || commit.ID != "commitsha" {
		t.Fatalf("response = %#v", result.Response)
	}
}

func TestGiteaReconcileWriteFileDifferentContentStaysAmbiguous(t *testing.T) {
	exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"path":"src/lib.rs","sha":"other","encoding":"base64","content":"bm90LW91cnM="}`))
	})
	defer cleanup()

	result, err := exec.Reconcile(context.Background(), Command{
		Type: "write_file", Repository: repo(), Path: "src/lib.rs", Branch: "agent/task-1",
		Content: []int{104, 101, 108, 108, 111}, Message: "update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconcileAmbiguous {
		t.Fatalf("status = %q", result.Status)
	}
}

func TestGiteaReconcileMergeUsesMergeStatusProbe(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		want   ReconcileStatus
	}{
		{"merged", http.StatusNoContent, ReconcileApplied},
		{"not merged", http.StatusNotFound, ReconcileNotApplied},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/repos/acme/widgets/pulls/17/merge" || r.Method != http.MethodGet {
					t.Fatalf("request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tt.status)
			})
			defer cleanup()
			head := "deadbeef"
			result, err := exec.Reconcile(context.Background(), Command{
				Type: "merge_change", Repository: repo(), Number: 17, Method: "squash", ExpectedHeadSHA: &head,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.want {
				t.Fatalf("status = %q", result.Status)
			}
			if tt.want == ReconcileApplied && result.Evidence.HeadSHA != "deadbeef" {
				t.Fatalf("evidence = %+v", result.Evidence)
			}
		})
	}
}

func TestGiteaReconcileCreateChangeFindsBaseHeadPair(t *testing.T) {
	exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.EscapedPath(), "/pulls/main/agent%2Ftask-1") {
			t.Fatalf("request = %s %s escaped=%s", r.Method, r.URL.Path, r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"number":42,"title":"Ship it","html_url":"https://git/pr/42","state":"open","head":{"ref":"agent/task-1","sha":"headsha"},"base":{"ref":"main"}}`))
	})
	defer cleanup()

	result, err := exec.Reconcile(context.Background(), Command{
		Type: "create_change", Repository: repo(), Head: "agent/task-1", Base: "main", Title: "Ship it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconcileApplied || result.Evidence.ChangeNumber != 42 || result.Evidence.HeadSHA != "headsha" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGiteaReconcileReviewMatchesStateBodyAndCommit(t *testing.T) {
	exec, cleanup := reconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":7,"body":"looks good","commit_id":"deadbeef","state":"APPROVED"}]`))
	})
	defer cleanup()
	commit := "deadbeef"
	result, err := exec.Reconcile(context.Background(), Command{
		Type: "review_change", Repository: repo(), Number: 17, Event: "approve", Body: "looks good", CommitID: &commit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconcileApplied || result.Evidence.ReviewID == nil || *result.Evidence.ReviewID != 7 {
		t.Fatalf("result = %+v", result)
	}
}
