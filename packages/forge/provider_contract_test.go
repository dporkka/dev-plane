package forge_test

import (
	"context"
	"testing"

	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/forge/contracttest"
	"github.com/ai-dev-control-plane/forge/forgetest"
)

func TestMemoryProviderContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) contracttest.Fixture {
		t.Helper()
		return contracttest.Fixture{
			Provider:   forgetest.NewProvider(),
			Credential: forge.Credential{Token: "test-token"},
			Repository: forge.Repository{Namespace: "acme", Name: "widget"},
		}
	})
}

func TestMemoryProviderRejectsInvalidMergeMethod(t *testing.T) {
	provider := forgetest.NewProvider()

	_, err := provider.MergeChange(
		context.Background(),
		forge.Credential{Token: "test-token"},
		forge.Repository{Namespace: "acme", Name: "widget"},
		1,
		forge.MergeChangeRequest{Method: forge.MergeMethod("octopus")},
	)
	if err == nil {
		t.Fatal("MergeChange() error = nil, want ErrInvalidRequest")
	}
	if !forge.IsInvalidRequest(err) {
		t.Fatalf("MergeChange() error = %v, want invalid request", err)
	}
}
