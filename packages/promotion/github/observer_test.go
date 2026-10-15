package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testSource    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testTarget    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testCandidate = "cccccccccccccccccccccccccccccccccccccccc"
	testTree      = "dddddddddddddddddddddddddddddddddddddddd"
)

func fakeServer(t *testing.T, mutate func(path string, payload map[string]any)) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	policy := "version: 1\nverification:\n  unit:\n    command: make test\n  integration:\n    command: make integration\nwork:\n  isolation: worktree\n  max_parallel_cost: 2\nreview:\n  independent: true\n  exact_head: true\n"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("observer must never perform %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		seen = append(seen, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "missing bearer auth", 401)
			return
		}
		var data map[string]any
		switch r.URL.Path {
		case "/repos/acme/widget/pulls/42":
			data = map[string]any{
				"state": "open", "draft": false, "mergeable": true,
				"head":             map[string]any{"sha": testSource},
				"base":             map[string]any{"ref": "main", "sha": testTarget},
				"user":             map[string]any{"login": "agent-bot"},
				"merge_commit_sha": testCandidate,
			}
		case "/repos/acme/widget/git/ref/heads/main":
			data = map[string]any{"object": map[string]any{"type": "commit", "sha": testTarget}}
		case "/repos/acme/widget/branches/main":
			data = map[string]any{"protected": true, "commit": map[string]any{"sha": testTarget}}
		case "/repos/acme/widget/git/commits/" + testCandidate:
			data = map[string]any{
				"sha": testCandidate, "tree": map[string]any{"sha": testTree},
				"parents": []map[string]string{{"sha": testTarget}, {"sha": testSource}},
			}
		case "/repos/acme/widget/contents/devplane.yaml":
			if r.URL.Query().Get("ref") != testTarget {
				http.Error(w, "untrusted policy ref", 400)
				return
			}
			data = map[string]any{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(policy))}
		case "/repos/acme/widget/pulls/42/reviews":
			data = map[string]any{}
			if r.URL.Query().Get("per_page") != "100" {
				http.Error(w, "unbounded pagination", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`[{"id":1,"state":"APPROVED","commit_id":"%s","submitted_at":"2026-10-15T10:00:00Z","user":{"login":"reviewer","type":"User"}}]`, testSource)))
			return
		default:
			http.Error(w, "unexpected path "+r.URL.Path, 404)
			return
		}
		if mutate != nil {
			mutate(r.URL.Path, data)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})
	return httptest.NewServer(handler), &seen
}

func observeForTest(t *testing.T, mutate func(path string, payload map[string]any)) (Observation, error) {
	t.Helper()
	server, _ := fakeServer(t, mutate)
	defer server.Close()
	reader := newReader(server.Client(), server.URL, "test-token")
	return reader.Observe(context.Background(), "acme/widget", 42)
}

func TestObserveUsesProtectedCurrentTargetAndVerifiedCandidateParents(t *testing.T) {
	obs, err := observeForTest(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Snapshot.CurrentSourceHeadSHA != testSource ||
		obs.Snapshot.CurrentTargetHeadSHA != testTarget ||
		obs.Snapshot.CandidateCommitSHA != testCandidate ||
		obs.Snapshot.CandidateTreeSHA != testTree {
		t.Fatalf("unexpected snapshot: %+v", obs.Snapshot)
	}
	if obs.Policy.Digest == "" || !strings.HasPrefix(obs.Policy.Digest, "sha256:") ||
		len(obs.Policy.RequiredGates) != 2 || obs.Policy.RequiredHumanApprovals != 1 {
		t.Fatalf("untrusted or incomplete policy: %+v", obs.Policy)
	}
	if len(obs.Approvals) != 1 || obs.Approvals[0].ReviewerID != "reviewer" || !obs.Approvals[0].Human ||
		obs.Approvals[0].CandidateCommitSHA != testCandidate || obs.AgentActorID != "agent-bot" {
		t.Fatalf("incorrect approval mapping: %+v", obs)
	}
}

func TestObserveRejectsUnprotectedBranch(t *testing.T) {
	_, err := observeForTest(t, func(path string, d map[string]any) {
		if strings.HasSuffix(path, "/branches/main") {
			d["protected"] = false
		}
	})
	if err == nil {
		t.Fatal("unprotected policies must not be trusted")
	}
}

func TestObserveRejectsBaseMovement(t *testing.T) {
	_, err := observeForTest(t, func(path string, d map[string]any) {
		if strings.Contains(path, "/git/ref/heads/") {
			d["object"] = map[string]any{"type": "commit", "sha": testSource}
		}
	})
	if err == nil {
		t.Fatal("stale base must not produce a trusted snapshot")
	}
}

func TestObserveRejectsWrongIntegrationParentsAndTrees(t *testing.T) {
	_, err := observeForTest(t, func(path string, d map[string]any) {
		if strings.Contains(path, "/git/commits/") {
			d["parents"] = []map[string]string{{"sha": testSource}, {"sha": testTarget}}
		}
	})
	if err == nil {
		t.Fatal("misbound integration candidate must not pass")
	}
	_, err = observeForTest(t, func(path string, d map[string]any) {
		if strings.Contains(path, "/git/commits/") {
			d["tree"] = map[string]any{"sha": ""}
		}
	})
	if err == nil {
		t.Fatal("missing immutable candidate tree must not pass")
	}
}

func TestObserveRejectsDraftOrMissingMergeCandidate(t *testing.T) {
	_, err := observeForTest(t, func(path string, d map[string]any) {
		if strings.HasSuffix(path, "/pulls/42") {
			d["draft"] = true
		}
	})
	if err == nil {
		t.Fatal("draft PR cannot be a promotable candidate")
	}
	_, err = observeForTest(t, func(path string, d map[string]any) {
		if strings.HasSuffix(path, "/pulls/42") {
			d["merge_commit_sha"] = nil
		}
	})
	if err == nil {
		t.Fatal("pending mergeability is not a verified candidate")
	}
}

func TestObserveDoesNotAllowPathInjection(t *testing.T) {
	server, _ := fakeServer(t, nil)
	defer server.Close()
	reader := newReader(server.Client(), server.URL, "test-token")
	for _, bad := range []string{"acme/../widget", "acme/widget/../../other", "acme/widget?ref=evil", "/acme/widget"} {
		if _, err := reader.Observe(context.Background(), bad, 42); err == nil {
			t.Fatalf("accepted invalid repo %q", bad)
		}
	}
}

func TestObserveRejectsMissingToken(t *testing.T) {
	server, _ := fakeServer(t, nil)
	defer server.Close()
	if _, err := newReader(server.Client(), server.URL, "").Observe(context.Background(), "acme/widget", 42); err == nil {
		t.Fatal("missing credentials must fail closed")
	}
}
