package prfactory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
)

func TestGiteaPRConfirmationRejectsChangedProviderState(t *testing.T) {
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tc := range []struct {
		name          string
		changed       bool
		wrongResponse bool
	}{
		{"exact state", false, false},
		{"changed after create", true, false},
		{"mismatched creation response", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/acme/widget/pulls/7" {
					t.Error("unexpected lookup")
				}
				sha := head
				if tc.changed {
					sha = base
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 70, "number": 7, "state": "open", "html_url": "http://" + r.Host + "/acme/widget/pulls/7", "head": map[string]any{"ref": "agent/task", "sha": sha, "repo": map[string]any{"id": 77}}, "base": map[string]any{"ref": "main", "sha": base, "repo": map[string]any{"id": 77}}})
			}))
			defer server.Close()
			forge, err := gateway.NewGiteaForge(server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			factory := &Factory{forge: forge, forgeCredential: gateway.ForgeCredential{AccessToken: "fixture-token"}}
			created := &gateway.ForgePullRequest{ID: 70, Number: 7, State: "open", HTMLURL: server.URL + "/acme/widget/pulls/7", Head: gateway.ForgeBranchRef{Ref: "agent/task", SHA: head}, Base: gateway.ForgeBranchRef{Ref: "main", SHA: base}}
			if tc.wrongResponse {
				created.Head.SHA = base
			}
			err = factory.confirmGiteaPR(context.Background(), repositoryTarget{Owner: "acme", Name: "widget"}, created, giteaPRRevision{headSHA: head, baseSHA: base, upstreamRepositoryID: 77}, "agent/task", "main")
			if tc.changed || tc.wrongResponse {
				if !errors.Is(err, ErrReviewedPush) {
					t.Fatalf("mismatch accepted: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.wrongResponse && calls != 0 || !tc.wrongResponse && calls != 1 {
				t.Fatalf("unexpected independent lookup count: %d", calls)
			}
		})
	}
}
