package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalCheckoutNulangSeederTransfersExactHeadIntoGuest(t *testing.T) {
	source := initLocalSourceRepo(t)
	head := gitOutputLocalSource(t, source, "rev-parse", "HEAD")
	var uploadedBytes int
	var extracted bool
	var verified bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/workspaces/ws-1/files/write-chunk":
			var request struct {
				Path          string `json:"path"`
				ContentBase64 string `json:"content_base64"`
				Sync          bool   `json:"sync"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode chunk request: %v", err)
			}
			if request.Path != ".seed/repository.tar" {
				t.Fatalf("chunk path = %q", request.Path)
			}
			data, err := base64.StdEncoding.DecodeString(request.ContentBase64)
			if err != nil {
				t.Fatalf("decode uploaded chunk: %v", err)
			}
			uploadedBytes += len(data)
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "chunk"})
		case "/workspaces/ws-1/exec":
			var request struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode exec request: %v", err)
			}
			stdout := ""
			switch request.Command {
			case "/bin/tar":
				extracted = true
			case "/bin/rm":
			case "git":
				if len(request.Args) == 2 && request.Args[0] == "rev-parse" && request.Args[1] == "HEAD" {
					verified = true
					stdout = head + "\n"
				}
			default:
				t.Fatalf("unexpected guest command %q %#v", request.Command, request.Args)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":          "exec",
				"exit_code":     0,
				"stdout_base64": base64.StdEncoding.EncodeToString([]byte(stdout)),
				"stderr_base64": "",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	seeder := NewLocalCheckoutNulangSeeder(source, head)
	if err := seeder.Seed(context.Background(), provider, "ws-1", CreateRequest{
		CloneURL: "https://ci-user:super-secret@example.com/org/repo.git",
	}); err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if uploadedBytes == 0 {
		t.Fatal("repository archive was not uploaded")
	}
	if !extracted || !verified {
		t.Fatalf("guest extraction/verification = extracted:%v verified:%v", extracted, verified)
	}
}

func TestLocalCheckoutNulangSeederRejectsChangedSourceBeforeGuestMutation(t *testing.T) {
	source := initLocalSourceRepo(t)
	originalHead := gitOutputLocalSource(t, source, "rev-parse", "HEAD")
	if err := osWriteFileForSeederTest(source, "SECOND.md", "drift\n"); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	runGitLocalSource(t, source, "add", "SECOND.md")
	runGitLocalSource(t, source, "commit", "-m", "drift")

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "token").WithHTTPClient(server.Client())
	err := NewLocalCheckoutNulangSeeder(source, originalHead).Seed(context.Background(), provider, "ws-1", CreateRequest{})
	if err == nil || !strings.Contains(err.Error(), "source HEAD mismatch") {
		t.Fatalf("Seed() error = %v, want source HEAD mismatch", err)
	}
	if called {
		t.Fatal("guest was mutated before source revision validation")
	}
}

func osWriteFileForSeederTest(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
