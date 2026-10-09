package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/runtimes"
)

// A fake owner holds canonical content. The two clients below must not each
// perform read/compare/write independently against this provider.
type revisionOwnerProvider struct {
	runtimes.Provider
	mu sync.Mutex
	content []byte
}

func (p *revisionOwnerProvider) ReadFile(context.Context, string, string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.content...), nil
}
func (p *revisionOwnerProvider) WriteFile(_ context.Context, _, _ string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.content = append([]byte(nil), data...)
	return nil
}

func TestRunnerConditionalFileWriteRejectsMissingAndStaleRevisions(t *testing.T) {
	owner := &revisionOwnerProvider{content: []byte("original")}
	h := NewHandler(owner, testLogger(t))
	router := chi.NewRouter()
	h.RegisterRoutes(router)

	doWrite := func(body, revision string, present bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/session/files-revision/src/main.ts", bytes.NewBufferString(body))
		if present {
			req.Header.Set("X-Dev-Plane-Expected-Revision", revision)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := doWrite("unguarded", "", false); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing precondition status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doWrite("invalid", "not-a-digest", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed revision status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doWrite("stale", runtimes.FileContentRevision([]byte("something else")), true); rec.Code != http.StatusConflict {
		t.Fatalf("stale precondition status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doWrite("created", "", true); rec.Code != http.StatusConflict {
		t.Fatalf("create-only should conflict with existing file: status=%d body=%s", rec.Code, rec.Body.String())
	}
	valid := runtimes.FileContentRevision([]byte("original"))
	rec := doWrite("accepted", valid, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching precondition status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Revision != runtimes.FileContentRevision([]byte("accepted")) {
		t.Fatalf("unexpected revision %q", payload.Revision)
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if string(owner.content) != "accepted" {
		t.Fatalf("unexpected file content %q", owner.content)
	}
}

func TestRunnerConditionalFileWriteRejectsEscapingPath(t *testing.T) {
	owner := &revisionOwnerProvider{content: []byte("safe")}
	h := NewHandler(owner, testLogger(t))
	router := chi.NewRouter()
	h.RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/session/files-revision/../../other", bytes.NewBufferString("unsafe"))
	req.Header.Set("X-Dev-Plane-Expected-Revision", runtimes.FileContentRevision([]byte("safe")))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("traversal unexpectedly admitted")
	}
}

func TestRunnerConditionalFileWriteRejectsOversizedPayload(t *testing.T) {
	owner := &revisionOwnerProvider{content: []byte("original")}
	h := NewHandler(owner, testLogger(t))
	router := chi.NewRouter()
	h.RegisterRoutes(router)
	tooBig := bytes.Repeat([]byte("x"), (8<<20)+1)
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/session/files-revision/big.ts", bytes.NewReader(tooBig))
	req.Header.Set("X-Dev-Plane-Expected-Revision", runtimes.FileContentRevision([]byte("original")))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized write status=%d body=%s", rec.Code, rec.Body.String())
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if string(owner.content) != "original" {
		t.Fatal("oversized request modified the file")
	}
}

func TestRunnerConditionalFileWriteHasOneWinnerAcrossClients(t *testing.T) {
	owner := &revisionOwnerProvider{content: []byte("before")}
	h := NewHandler(owner, testLogger(t))
	router := chi.NewRouter()
	h.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	rev := runtimes.FileContentRevision([]byte("before"))
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, data := range []string{"client-A", "client-B"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPut, server.URL+"/v1/workspaces/session/files-revision/main.ts", bytes.NewBufferString(value))
			if err != nil { codes <- -1; return }
			req.Header.Set("X-Dev-Plane-Expected-Revision", rev)
			resp, err := http.DefaultClient.Do(req)
			if err != nil { codes <- -1; return }
			defer resp.Body.Close()
			codes <- resp.StatusCode
		}(data)
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		switch code {
		case http.StatusOK: success++
		case http.StatusConflict: conflict++
		default: t.Fatalf("unexpected status: %d", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("runner did not serialize requests: successes=%d conflicts=%d", success, conflict)
	}
}
