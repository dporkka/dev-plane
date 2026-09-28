package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNulangCloudCreateWorkspace(t *testing.T) {
	var auth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/workspaces" { t.Fatalf("request = %s %s", r.Method, r.URL.Path) }
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		if body["id"] != "task-123" { t.Fatalf("id = %v", body["id"]) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspace":{"workspace_id":"task-123","status":"running"},"guest_ready":true}`))
	}))
	defer s.Close()

	p := NewNulangCloudProvider(s.URL, "secret")
	got, err := p.CreateWorkspace(context.Background(), CreateRequest{WorktreeName:"Task 123"})
	if err != nil { t.Fatal(err) }
	if auth != "Bearer secret" { t.Fatalf("authorization = %q", auth) }
	if got.ID != "task-123" || got.Provider != "nulang-cloud" || got.Status != "ready" { t.Fatalf("session = %+v", got) }
}

func TestNulangCloudExecuteCommand(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspaces/w1/exec" { t.Fatalf("path = %s", r.URL.Path) }
		var body struct { Command string `json:"command"`; Args []string `json:"args"`; Cwd string `json:"cwd"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		if body.Command != "go" || len(body.Args) != 1 || body.Args[0] != "test" || body.Cwd != "/workspace" { t.Fatalf("body = %+v", body) }
		_, _ = w.Write([]byte(`{"kind":"exec","exit_code":0,"timed_out":false,"stdout_base64":"`+base64.StdEncoding.EncodeToString([]byte("ok\n"))+`","stderr_base64":""}`))
	}))
	defer s.Close()

	got, err := NewNulangCloudProvider(s.URL, "").ExecuteCommand(context.Background(), "w1", Command{Args:[]string{"go","test"}, Dir:"/workspace", Timeout:time.Second})
	if err != nil { t.Fatal(err) }
	if got.ExitCode != 0 || got.Stdout != "ok\n" { t.Fatalf("result = %+v", got) }
}

func TestNulangCloudExecuteCommandTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"kind":"exec","exit_code":null,"timed_out":true,"stdout_base64":"","stderr_base64":""}`))
	}))
	defer s.Close()
	_, err := NewNulangCloudProvider(s.URL, "").ExecuteCommand(context.Background(), "w1", Command{Args:[]string{"sleep","60"}})
	if !errors.Is(err, ErrCommandTimeout) { t.Fatalf("error = %v", err) }
}

func TestNulangCloudFileCheckpointRestoreAndStatus(t *testing.T) {
	var restored bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/workspaces/w1/files/write":
			var body struct { Path, Content string; CreateParents bool `json:"create_parents"` }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Path != "a.txt" || body.Content != base64.StdEncoding.EncodeToString([]byte("hello")) || !body.CreateParents { t.Fatalf("write = %+v", body) }
			_, _ = w.Write([]byte(`{"kind":"ack"}`))
		case r.URL.Path == "/workspaces/w1/files/read":
			_, _ = w.Write([]byte(`{"kind":"file","path":"a.txt","size":5,"content_base64":"aGVsbG8="}`))
		case r.URL.Path == "/workspaces/w1/checkpoints":
			_, _ = w.Write([]byte(`{"checkpoint":{"id":"cp1","workspace_id":"w1","created_at_ms":1000},"restarted":true}`))
		case r.URL.Path == "/workspaces/w1/checkpoints/cp1/restore":
			restored = true; _, _ = w.Write([]byte(`{"checkpoint":{"id":"cp1","workspace_id":"w1","created_at_ms":1000},"restarted":true}`))
		case r.URL.Path == "/workspaces/w1":
			_, _ = w.Write([]byte(`{"id":"w1","status":"running","guest_ready":true,"memory_mb":2048}`))
		default: t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer s.Close()

	p := NewNulangCloudProvider(s.URL, "")
	if err := p.WriteFile(context.Background(),"w1","a.txt",[]byte("hello")); err != nil { t.Fatal(err) }
	b, err := p.ReadFile(context.Background(),"w1","a.txt"); if err != nil || string(b)!="hello" { t.Fatalf("read = %q, %v", b, err) }
	snap, err := p.Snapshot(context.Background(),"w1"); if err != nil || snap.ID!="cp1" { t.Fatalf("snapshot = %+v, %v", snap, err) }
	if err := p.Restore(context.Background(),"w1",snap); err != nil || !restored { t.Fatalf("restore = %v restored=%v", err, restored) }
	st, err := p.GetStatus(context.Background(),"w1"); if err != nil || st.Status!="ready" || st.MemoryUsage != 2048<<20 { t.Fatalf("status = %+v, %v", st, err) }
}

func TestNulangCloudNotFoundMapsToProviderError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w,r) }))
	defer s.Close()
	err := NewNulangCloudProvider(s.URL, "").DestroyWorkspace(context.Background(),"missing")
	if !errors.Is(err, ErrSessionNotFound) { t.Fatalf("error = %v", err) }
}


func TestNulangCloudCreateWorkspaceSeedsRepositoryInBoundedChunks(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seeded"), 0o644); err != nil { t.Fatal(err) }
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil { t.Fatal(err) }

	var chunks int
	var uploadedBytes int64
	var sawExtract, sawRemove, sawSync bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
			_, _ = w.Write([]byte("{\"workspace\":{\"workspace_id\":\"seed\",\"status\":\"running\"},\"guest_ready\":true}"))
		case r.URL.Path == "/workspaces/seed/files/write-chunk":
			var body struct {
				Path string `json:"path"`
				Content string `json:"content_base64"`
				Offset int64 `json:"offset"`
				Truncate bool `json:"truncate"`
				Sync bool `json:"sync"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
			if body.Path != ".devplane/repo.tar" { t.Fatalf("path = %q", body.Path) }
			data, err := base64.StdEncoding.DecodeString(body.Content); if err != nil { t.Fatal(err) }
			if len(data) > nulangCloudSeedChunkBytes { t.Fatalf("chunk = %d", len(data)) }
			if chunks == 0 && !body.Truncate { t.Fatal("first chunk must truncate") }
			if body.Sync {
				sawSync = true
				if len(data) != 0 { t.Fatalf("final sync wrote %d bytes, want 0", len(data)) }
				if body.Offset != uploadedBytes { t.Fatalf("final sync offset = %d, want %d", body.Offset, uploadedBytes) }
				if body.Truncate { t.Fatal("final sync must not truncate the uploaded archive") }
			} else {
				if body.Offset != uploadedBytes { t.Fatalf("chunk offset = %d, want %d", body.Offset, uploadedBytes) }
				uploadedBytes += int64(len(data))
			}
			chunks++
			_, _ = w.Write([]byte("{\"kind\":\"chunk\",\"path\":\".devplane/repo.tar\",\"offset\":0,\"bytes_written\":1,\"size\":1}"))
		case r.URL.Path == "/workspaces/seed/exec":
			var body struct { Command string `json:"command"`; Args []string `json:"args"` }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
			if body.Command == "tar" { sawExtract = true }
			if body.Command == "rm" { sawRemove = true }
			_, _ = w.Write([]byte("{\"kind\":\"exec\",\"exit_code\":0,\"timed_out\":false,\"stdout_base64\":\"\",\"stderr_base64\":\"\"}"))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer s.Close()

	p := NewNulangCloudProvider(s.URL, "")
	p.cloneRepository = func(context.Context, CreateRequest) (string, error) { return repo, nil }
	got, err := p.CreateWorkspace(context.Background(), CreateRequest{WorktreeName:"seed", CloneURL:"https://example.invalid/repo.git"})
	if err != nil { t.Fatal(err) }
	if got.ID != "seed" || chunks == 0 || !sawSync || !sawExtract || !sawRemove {
		t.Fatalf("session=%+v chunks=%d sync=%v extract=%v remove=%v", got, chunks, sawSync, sawExtract, sawRemove)
	}
}

func TestNulangCloudCreateWorkspaceDestroysWorkspaceWhenSeedFails(t *testing.T) {
	var destroyed bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/workspaces" {
			_, _ = w.Write([]byte("{\"workspace\":{\"workspace_id\":\"seed-fail\",\"status\":\"running\"},\"guest_ready\":true}"))
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/workspaces/seed-fail" {
			destroyed = true
			_, _ = w.Write([]byte("{}"))
			return
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer s.Close()

	p := NewNulangCloudProvider(s.URL, "")
	p.cloneRepository = func(context.Context, CreateRequest) (string, error) { return "", errors.New("clone failed") }
	_, err := p.CreateWorkspace(context.Background(), CreateRequest{WorktreeName:"seed-fail", CloneURL:"https://example.invalid/repo.git"})
	if err == nil || !destroyed { t.Fatalf("error=%v destroyed=%v", err, destroyed) }
}
