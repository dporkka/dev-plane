package forgeexec

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func githubReconcileServer(t *testing.T, handler http.HandlerFunc) (*GitHubExecutor, func()) {
	t.Helper()
	s := httptest.NewServer(handler)
	return NewGitHubExecutor(s.URL, "token-123", s.Client()), s.Close
}

func TestGitHubReconcileCreateBranchApplied(t *testing.T) {
	exec, cleanup := githubReconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/git/ref/heads/agent/task-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ref":"refs/heads/agent/task-1","object":{"sha":"deadbeef"}}`))
	})
	defer cleanup()

	result, err := exec.Reconcile(context.Background(), Command{
		Type:"create_branch", Repository:repo(), Name:"agent/task-1", From:"main",
	})
	if err != nil { t.Fatal(err) }
	if result.Status != ReconcileApplied || result.Response == nil || result.Evidence.HeadSHA != "deadbeef" || result.Evidence.Provider!="github" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGitHubReconcileCreateBranchAbsentIsRetryable(t *testing.T) {
	exec, cleanup := githubReconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w,r)
	})
	defer cleanup()
	result,err:=exec.Reconcile(context.Background(),Command{Type:"create_branch",Repository:repo(),Name:"agent/task-1",From:"main"})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileNotApplied {t.Fatalf("status=%q",result.Status)}
}

func TestGitHubReconcileWriteFileMatchesContentAndCapturesBranchHead(t *testing.T) {
	exec, cleanup := githubReconcileServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path,"/contents/src/lib.rs"):
			if r.URL.Query().Get("ref")!="agent/task-1" {t.Fatalf("ref=%q",r.URL.Query().Get("ref"))}
			_,_=w.Write([]byte(`{"type":"file","sha":"blobsha","encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte("hello"))+`"}`))
		case strings.Contains(r.URL.Path,"/git/ref/heads/agent/task-1"):
			_,_=w.Write([]byte(`{"ref":"refs/heads/agent/task-1","object":{"sha":"commitsha"}}`))
		default:
			http.NotFound(w,r)
		}
	})
	defer cleanup()
	result,err:=exec.Reconcile(context.Background(),Command{
		Type:"write_file",Repository:repo(),Path:"src/lib.rs",Branch:"agent/task-1",
		Content:[]int{104,101,108,108,111},Message:"update",
	})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileApplied || result.Evidence.HeadSHA!="commitsha" || result.Evidence.BlobSHA!="blobsha" {
		t.Fatalf("result=%+v",result)
	}
}

func TestGitHubReconcileWriteFileDifferentContentStaysAmbiguous(t *testing.T) {
	exec,cleanup:=githubReconcileServer(t,func(w http.ResponseWriter,r *http.Request){
		_,_=w.Write([]byte(`{"type":"file","sha":"other","encoding":"base64","content":"bm90LW91cnM="}`))
	})
	defer cleanup()
	result,err:=exec.Reconcile(context.Background(),Command{
		Type:"write_file",Repository:repo(),Path:"src/lib.rs",Branch:"agent/task-1",
		Content:[]int{104,101,108,108,111},Message:"update",
	})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileAmbiguous {t.Fatalf("status=%q",result.Status)}
}

func TestGitHubReconcileCreateChangeFindsUniqueBaseHeadPair(t *testing.T) {
	exec,cleanup:=githubReconcileServer(t,func(w http.ResponseWriter,r *http.Request){
		if r.Method!=http.MethodGet || r.URL.Path!="/repos/acme/widgets/pulls" {t.Fatalf("request=%s %s",r.Method,r.URL.Path)}
		if r.URL.Query().Get("state")!="all" || r.URL.Query().Get("head")!="acme:agent/task-1" || r.URL.Query().Get("base")!="main" {
			t.Fatalf("query=%s",r.URL.RawQuery)
		}
		_,_=w.Write([]byte(`[{"number":42,"title":"Ship it","html_url":"https://github.test/acme/widgets/pull/42","state":"open","head":{"ref":"agent/task-1","sha":"headsha"},"base":{"ref":"main"}}]`))
	})
	defer cleanup()
	result,err:=exec.Reconcile(context.Background(),Command{
		Type:"create_change",Repository:repo(),Head:"agent/task-1",Base:"main",Title:"Ship it",
	})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileApplied || result.Evidence.ChangeNumber!=42 || result.Evidence.HeadSHA!="headsha" {
		t.Fatalf("result=%+v",result)
	}
}

func TestGitHubReconcileCreateChangeAbsentIsRetryable(t *testing.T) {
	exec,cleanup:=githubReconcileServer(t,func(w http.ResponseWriter,r *http.Request){_,_=w.Write([]byte("[]"))})
	defer cleanup()
	result,err:=exec.Reconcile(context.Background(),Command{
		Type:"create_change",Repository:repo(),Head:"agent/task-1",Base:"main",Title:"Ship it",
	})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileNotApplied {t.Fatalf("result=%+v",result)}
}

func TestGitHubReconcileCreateChangeMultipleMatchesStayAmbiguous(t *testing.T) {
	exec,cleanup:=githubReconcileServer(t,func(w http.ResponseWriter,r *http.Request){
		_,_=w.Write([]byte(`[
			{"number":42,"title":"Ship it","state":"open","head":{"ref":"agent/task-1","sha":"a"},"base":{"ref":"main"}},
			{"number":43,"title":"Ship it","state":"closed","head":{"ref":"agent/task-1","sha":"b"},"base":{"ref":"main"}}
		]`))
	})
	defer cleanup()
	result,err:=exec.Reconcile(context.Background(),Command{
		Type:"create_change",Repository:repo(),Head:"agent/task-1",Base:"main",Title:"Ship it",
	})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileAmbiguous {t.Fatalf("result=%+v",result)}
}

func TestGitHubReconcileReviewMatchesProviderStateBodyAndCommit(t *testing.T) {
	exec,cleanup:=githubReconcileServer(t,func(w http.ResponseWriter,r *http.Request){
		_,_=w.Write([]byte(`[{"id":7,"body":"looks good","commit_id":"deadbeef","state":"APPROVED"}]`))
	})
	defer cleanup()
	commit:="deadbeef"
	result,err:=exec.Reconcile(context.Background(),Command{
		Type:"review_change",Repository:repo(),Number:17,Event:"approve",Body:"looks good",CommitID:&commit,
	})
	if err!=nil {t.Fatal(err)}
	if result.Status!=ReconcileApplied || result.Evidence.ReviewID==nil || *result.Evidence.ReviewID!=7 || result.Evidence.HeadSHA!="deadbeef" {
		t.Fatalf("result=%+v",result)
	}
}

func TestGitHubReconcileMergeUsesMergedStatusProbe(t *testing.T) {
	for _,tt:=range []struct{name string; status int; want ReconcileStatus}{
		{"merged",http.StatusNoContent,ReconcileApplied},
		{"not merged",http.StatusNotFound,ReconcileNotApplied},
	}{
		t.Run(tt.name,func(t *testing.T){
			exec,cleanup:=githubReconcileServer(t,func(w http.ResponseWriter,r *http.Request){
				if r.Method!=http.MethodGet || r.URL.Path!="/repos/acme/widgets/pulls/17/merge" {t.Fatalf("request=%s %s",r.Method,r.URL.Path)}
				w.WriteHeader(tt.status)
			})
			defer cleanup()
			head:="deadbeef"
			result,err:=exec.Reconcile(context.Background(),Command{Type:"merge_change",Repository:repo(),Number:17,Method:"squash",ExpectedHeadSHA:&head})
			if err!=nil {t.Fatal(err)}
			if result.Status!=tt.want {t.Fatalf("status=%q",result.Status)}
		})
	}
}
