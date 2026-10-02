// Package webui serves the Vite SPA from the Go control-plane binary.
package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const browserAuthPath = "/api/auth/github/callback"

// Wrap returns the single-origin production handler: API and health requests
// stay on the Go router while every other browser route is served by the
// embedded Vite application. Production startup fails when the Vite bundle was
// not built into the binary.
func Wrap(api http.Handler) (http.Handler, error) {
	assets, err := embeddedAssets()
	if err != nil {
		return nil, err
	}
	return WrapWithAssets(api, assets), nil
}

// WrapWithAssets is Wrap with an injectable filesystem for tests.
func WrapWithAssets(api http.Handler, assets fs.FS) http.Handler {
	static := NewStaticHandler(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == browserAuthPath {
			serveGitHubAuthBridge(w, r, api)
			return
		}
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}

func isAPIPath(requestPath string) bool {
	return requestPath == "/api" ||
		strings.HasPrefix(requestPath, "/api/") ||
		requestPath == "/health" ||
		requestPath == "/ready"
}

// NewStaticHandler serves immutable Vite assets directly and falls back to
// index.html for extensionless client-side routes.
func NewStaticHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean != "" && clean != "." {
			if info, err := fs.Stat(assets, clean); err == nil && !info.IsDir() {
				if strings.HasPrefix(clean, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}

			// Missing asset URLs must remain 404s. Returning index.html here would
			// turn a stale JS chunk request into an HTML parse failure.
			if path.Ext(clean) != "" {
				http.NotFound(w, r)
				return
			}
		}

		if _, err := fs.Stat(assets, "index.html"); err != nil {
			http.Error(w, "web UI is not built", http.StatusServiceUnavailable)
			return
		}

		clone := r.Clone(r.Context())
		urlCopy := *r.URL
		urlCopy.Path = "/"
		clone.URL = &urlCopy
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, clone)
	})
}

// serveGitHubAuthBridge preserves the browser OAuth contract that previously
// lived in a Next.js route. The Go API still owns OAuth state and token
// exchange; this adapter only rewrites the browser-facing path and stores the
// resulting JWT on the same origin as the SPA.
func serveGitHubAuthBridge(w http.ResponseWriter, r *http.Request, api http.Handler) {
	if token := r.URL.Query().Get("token"); token != "" {
		renderTokenCallback(w, token)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		api.ServeHTTP(w, cloneRequestPath(r, "/api/v1/auth/github", false))
		return
	}

	capture := newResponseCapture()
	api.ServeHTTP(capture, cloneRequestPath(r, "/api/v1/auth/github/callback", true))
	if capture.statusCode < 200 || capture.statusCode >= 300 {
		capture.copyTo(w)
		return
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(capture.body.Bytes(), &payload); err != nil || payload.Token == "" {
		http.Error(w, "GitHub sign-in did not return a token", http.StatusBadGateway)
		return
	}

	for _, value := range capture.header.Values("Set-Cookie") {
		w.Header().Add("Set-Cookie", value)
	}
	renderTokenCallback(w, payload.Token)
}

func cloneRequestPath(r *http.Request, requestPath string, preserveQuery bool) *http.Request {
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = requestPath
	if !preserveQuery {
		urlCopy.RawQuery = ""
	}
	clone.URL = &urlCopy
	return clone
}

func renderTokenCallback(w http.ResponseWriter, token string) {
	encoded, _ := json.Marshal(token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(
		"Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'",
	)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
  <head><meta charset="utf-8"><title>Signing in</title></head>
  <body>
    <p>Signing you in...</p>
    <script>
      localStorage.setItem("token", %s);
      window.location.replace("/dashboard");
    </script>
  </body>
</html>`, encoded)
}

type responseCapture struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func newResponseCapture() *responseCapture {
	return &responseCapture{header: make(http.Header), statusCode: http.StatusOK}
}

func (c *responseCapture) Header() http.Header { return c.header }

func (c *responseCapture) WriteHeader(statusCode int) {
	c.statusCode = statusCode
}

func (c *responseCapture) Write(p []byte) (int, error) {
	return c.body.Write(p)
}

func (c *responseCapture) copyTo(w http.ResponseWriter) {
	for key, values := range c.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(c.statusCode)
	_, _ = w.Write(c.body.Bytes())
}
