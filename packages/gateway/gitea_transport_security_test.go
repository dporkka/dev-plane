package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGiteaForgeRefusesRedirectWithoutMutatingCallerClient(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-target" {
			redirected.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = 30 * time.Second
	forge, err := NewGiteaForge(server.URL, client)
	if err != nil {
		t.Fatal(err)
	}
	credential := ForgeCredential{AccessToken: "secret-token"}
	if _, err := forge.GetRepository(context.Background(), credential, "acme", "widget"); err == nil {
		t.Error("redirected GET must fail")
	}
	if _, err := forge.MergePullRequest(context.Background(), credential, "acme", "widget", 1, ForgeMergeRequest{ExpectedHeadSHA: "abc123"}); err == nil {
		t.Error("redirected mutation must fail")
	}
	if got := redirected.Load(); got != 0 {
		t.Errorf("redirect destination received %d requests, want zero", got)
	}
	if client.CheckRedirect != nil || client.Timeout != 30*time.Second {
		t.Error("forge mutated caller-owned HTTP client")
	}
}

func TestGiteaForgeErrorDoesNotExposeProviderBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("echoed credential: secret-token"))
	}))
	defer server.Close()
	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = forge.GetRepository(context.Background(), ForgeCredential{AccessToken: "secret-token"}, "acme", "widget")
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("expected sanitized HTTP status error, got %v", err)
	}
}

func TestGiteaForgeRejectsOversizedOrTrailingResponse(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"oversized", `{"name":"` + strings.Repeat("x", 4*1024*1024) + `"}`},
		{"trailing JSON", `{"name":"widget"}{"name":"other"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			forge, err := NewGiteaForge(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := forge.GetRepository(context.Background(), ForgeCredential{}, "acme", "widget"); err == nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
}

func TestGiteaForgeRedactsInvalidInstanceCredentials(t *testing.T) {
	for _, raw := range []string{
		"https://user:secret-token@git.example",
		"https://git.example?token=secret-token",
		"https://git.example/#secret-token",
	} {
		_, err := NewGiteaForge(raw, nil)
		if err == nil || strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("expected sanitized instance configuration error, got %v", err)
		}
	}
}
