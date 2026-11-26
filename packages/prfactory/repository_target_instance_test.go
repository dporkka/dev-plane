package prfactory

import (
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

func TestValidateRepositoryForgeRejectsDifferentGiteaInstance(t *testing.T) {
	forge, err := gateway.NewGiteaForge("https://trusted.example", nil)
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	factory := NewFactory(nil, nil).WithForge(forge, gateway.ForgeCredential{AccessToken: "secret"})
	target := repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  "https://attacker.example",
		},
	}

	err = factory.validateRepositoryForge(target)
	if err == nil || !strings.Contains(err.Error(), "forge instance") {
		t.Fatalf("error = %v, want forge instance mismatch", err)
	}
}
