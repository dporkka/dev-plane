package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTasksReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks/t1/readiness" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"attention","checks":[]}`))
	}))
	defer server.Close()
	cleanup := setupTestConfig(t, server.URL)
	defer cleanup()

	if err := runTasksReadiness([]string{"t1"}); err != nil {
		t.Fatalf("tasks readiness: %v", err)
	}
}

func TestTasksReadinessRequiresTaskID(t *testing.T) {
	if err := runTasksReadiness(nil); err == nil {
		t.Fatal("tasks readiness error = nil, want usage error")
	}
}
