package prfactory

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

func TestGetRepositoryTargetReadsForgeSettings(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT owner, name, settings FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "settings"}).AddRow(
			"acme", "widget", `{"forge":{"provider":"gitea","base_url":"https://git.example.test"}}`,
		))

	factory := NewFactory(db, nil)
	target, err := factory.getRepositoryTarget(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("get repository target: %v", err)
	}
	if target.Owner != "acme" || target.Name != "widget" {
		t.Fatalf("target = %+v", target)
	}
	if target.Forge.Provider != models.ForgeProviderGitea || target.Forge.BaseURL != "https://git.example.test" {
		t.Fatalf("forge = %+v", target.Forge)
	}
}

func TestValidateRepositoryForgeRejectsProviderMismatch(t *testing.T) {
	factory := NewFactory(nil, nil).WithForge(fakeForge{name: "github"}, gateway.ForgeCredential{AccessToken: "token"})
	err := factory.validateRepositoryForge(repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{Provider: models.ForgeProviderGitea, BaseURL: "https://git.example.test"},
	})
	if err == nil || !strings.Contains(err.Error(), "repository requires forge provider gitea") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRepositoryForgeAcceptsMatchingProvider(t *testing.T) {
	factory := NewFactory(nil, nil).WithForge(fakeForge{name: "gitea"}, gateway.ForgeCredential{AccessToken: "token"})
	if err := factory.validateRepositoryForge(repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{Provider: models.ForgeProviderGitea, BaseURL: "https://git.example.test"},
	}); err != nil {
		t.Fatalf("validate forge: %v", err)
	}
}
