package forge_test

import (
	"testing"

	"github.com/ai-dev-control-plane/forge"
)

func TestParseRepositoryFullName(t *testing.T) {
	tests := []struct {
		name      string
		fullName  string
		namespace string
		repo      string
		wantErr   bool
	}{
		{name: "owner repo", fullName: "acme/widget", namespace: "acme", repo: "widget"},
		{name: "nested namespace", fullName: "acme/platform/widget", namespace: "acme/platform", repo: "widget"},
		{name: "trims whitespace", fullName: "  acme/platform/widget  ", namespace: "acme/platform", repo: "widget"},
		{name: "missing namespace", fullName: "widget", wantErr: true},
		{name: "missing repository", fullName: "acme/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := forge.ParseRepositoryFullName(tt.fullName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParseRepositoryFullName() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRepositoryFullName() error = %v", err)
			}
			if got.Namespace != tt.namespace || got.Name != tt.repo {
				t.Fatalf("repository = %+v, want %s/%s", got, tt.namespace, tt.repo)
			}
		})
	}
}
