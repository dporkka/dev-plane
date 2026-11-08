package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWoodpeckerConfigurationAcceptsDocumentedByteArrayAndRejectsInvalidElements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		valid  bool
	}{
		{"documented array", `[{"name":".woodpecker.yml","hash":"server-hash","data":[115,116,101,112,115,58,10]}]`, true},
		{"invalid byte", `[{"name":".woodpecker.yml","data":[300]}]`, false},
		{"negative byte", `[{"name":".woodpecker.yml","data":[-1]}]`, false},
		{"fractional byte", `[{"name":".woodpecker.yml","data":[1.5]}]`, false},
		{"null bytes", `[{"name":".woodpecker.yml","data":null}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/repos/11":
					_, _ = w.Write([]byte(`{"id":11,"full_name":"dporkka/dev-plane"}`))
				case "/api/repos/11/pipelines/23":
					p := map[string]any{"id": 81, "number": 23, "commit": strings.Repeat("a", 40),
						"rerun_count": 0, "status": "success",
						"workflows": []any{map[string]any{"id": 9, "agent_id": 3,
							"children": []any{map[string]any{"id": 7, "name": "test", "state": "success", "exit_code": 0, "started": 1600000006, "finished": 1600000018}}}}}
					_ = json.NewEncoder(w).Encode(p)
				case "/api/repos/11/pipelines/23/config":
					_, _ = w.Write([]byte(tc.config))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			reader := WoodpeckerReader{BaseURL: server.URL, Token: "test-token", RepositoryID: 11, Repository: "dporkka/dev-plane"}
			got, err := reader.ObserveAttempt(context.Background(), RemoteCIQuery{
				HeadSHA: strings.Repeat("a", 40), PipelineID: "81", RunID: "23", JobID: "7", AttemptID: "0",
			})
			if tc.valid {
				if err != nil {
					t.Fatalf("documented config byte array rejected: %v", err)
				}
				if got.ConfigurationSHA256 == "" {
					t.Fatal("missing executed config digest")
				}
			} else if err == nil {
				t.Fatalf("malformed Woodpecker config accepted: %+v", got)
			}
		})
	}
}

func TestWoodpeckerConfigDigestDoesNotDependOnFileOrder(t *testing.T) {
	first := []wpConfig{{Name: "a", Data: []byte("one")}, {Name: "b", Data: []byte("two")}}
	second := []wpConfig{{Name: "b", Data: []byte("two")}, {Name: "a", Data: []byte("one")}}
	a, err := configDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := configDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("digest changed with config order %s %s", a, b)
	}
	if _, err := hex.DecodeString(a); err != nil || len(a) != sha256.Size*2 {
		t.Fatalf("invalid digest %s", a)
	}
}
