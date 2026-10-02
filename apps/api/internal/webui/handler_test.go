package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/app-abc123.js": &fstest.MapFile{Data: []byte("console.log('ok')")},
	}
}

func TestStaticHandlerFallsBackToIndexForClientRoute(t *testing.T) {
	h := NewStaticHandler(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/projects/project-123/tasks", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Fatalf("expected SPA index, got %q", rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected no-cache index, got %q", got)
	}
}

func TestStaticHandlerServesHashedAssetWithLongCache(t *testing.T) {
	h := NewStaticHandler(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected cache header %q", got)
	}
}

func TestStaticHandlerDoesNotMaskMissingAsset(t *testing.T) {
	h := NewStaticHandler(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestWrapKeepsAPIOnGoRouter(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-API-Path", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	h := WrapWithAssets(api, testAssets())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/123", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected API response, got %d", rr.Code)
	}
	if got := rr.Header().Get("X-API-Path"); got != "/api/v1/tasks/123" {
		t.Fatalf("API received wrong path %q", got)
	}
}

func TestGitHubAuthBridgeInitiatesThroughGoAPI(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/github" {
			t.Fatalf("expected rewritten auth path, got %q", r.URL.Path)
		}
		http.Redirect(w, r, "https://github.com/login/oauth/authorize", http.StatusTemporaryRedirect)
	})
	h := WrapWithAssets(api, testAssets())
	req := httptest.NewRequest(http.MethodGet, browserAuthPath, nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected OAuth redirect, got %d", rr.Code)
	}
}

func TestGitHubAuthBridgeStoresCallbackToken(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/github/callback" {
			t.Fatalf("expected rewritten callback path, got %q", r.URL.Path)
		}
		w.Header().Add("Set-Cookie", "oauth_state=; Max-Age=0; Path=/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"signed.jwt.value"}`))
	})
	h := WrapWithAssets(api, testAssets())
	req := httptest.NewRequest(
		http.MethodGet,
		browserAuthPath+"?code=abc&state=state-123",
		nil,
	)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `localStorage.setItem("token", "signed.jwt.value")`) {
		t.Fatalf("callback did not store token: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `window.location.replace("/dashboard")`) {
		t.Fatalf("callback did not redirect to dashboard")
	}
	if got := rr.Header().Get("Set-Cookie"); !strings.Contains(got, "oauth_state=") {
		t.Fatalf("expected OAuth state cookie to be cleared, got %q", got)
	}
}
