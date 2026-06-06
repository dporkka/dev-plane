package handlers

import (
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/policies"
)

func setupTest(t *testing.T) (*Handler, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
	h := NewHandler(db, slog.Default()).WithCapabilityKernel(capability.NewKernel(allowAll, nil, nil, slog.Default()))
	cleanup := func() { db.Close() }
	return h, mock, cleanup
}
