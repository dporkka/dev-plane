package runtimes

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestNulangProviderConformanceRejectsDifferentKeyForExistingWorkspace(t *testing.T) {
	authority := &fakeWorkspaceAuthority{}
	server := httptest.NewServer(authority.handler(false))
	defer server.Close()
	seeder := &conformanceSeeder{}
	provider := NewNulangCloudProvider(server.URL, "internal-secret").
		WithHTTPClient(server.Client()).
		WithRepositorySeeder(seeder)

	req := CreateRequest{
		RepositoryID:   "repo-1",
		CloneURL:       "https://example.invalid/repo.git",
		Branch:         "agent/task-1/initial",
		BaseBranch:     "main",
		WorktreeName:   "ws-conformance",
		IdempotencyKey: "workspace:run-1",
		Limits: ResourceLimits{
			CPUMillis: 2000,
			MemoryMB:  4096,
			DiskMB:    nulangWorkspaceDiskMB,
		},
	}
	if _, err := provider.CreateWorkspace(context.Background(), req); err != nil {
		t.Fatalf("first CreateWorkspace() error = %v", err)
	}

	conflict := req
	conflict.IdempotencyKey = "workspace:run-2"
	if _, err := provider.CreateWorkspace(context.Background(), conflict); err == nil {
		t.Fatal("different key for existing workspace succeeded, want conflict")
	}
	if len(authority.createIntent) == 0 || authority.idempotencyKey != req.IdempotencyKey {
		t.Fatalf("original authority was mutated: key=%q intent=%v", authority.idempotencyKey, authority.createIntent)
	}
}

func TestNulangProviderConformanceRejectsChangedResourcesForSameKey(t *testing.T) {
	authority := &resourceFencingWorkspaceAuthority{}
	server := httptest.NewServer(authority.handler())
	defer server.Close()
	provider := NewNulangCloudProvider(server.URL, "internal-secret").
		WithHTTPClient(server.Client()).
		WithRepositorySeeder(&conformanceSeeder{})

	req := CreateRequest{
		RepositoryID:   "repo-1",
		CloneURL:       "https://example.invalid/repo.git",
		Branch:         "agent/task-1/initial",
		BaseBranch:     "main",
		WorktreeName:   "ws-conformance",
		IdempotencyKey: "workspace:run-1",
		Limits: ResourceLimits{
			CPUMillis: 2000,
			MemoryMB:  4096,
			DiskMB:    nulangWorkspaceDiskMB,
		},
	}
	if _, err := provider.CreateWorkspace(context.Background(), req); err != nil {
		t.Fatalf("first CreateWorkspace() error = %v", err)
	}

	changed := req
	changed.Limits.MemoryMB = 8192
	if _, err := provider.CreateWorkspace(context.Background(), changed); err == nil {
		t.Fatal("same key with changed resources succeeded, want conflict")
	}
}
