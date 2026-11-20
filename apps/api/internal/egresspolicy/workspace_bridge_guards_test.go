package egresspolicy

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type mutableSessionResolver struct {
	calls       int
	revokeAfter int
}

func (r *mutableSessionResolver) ResolveAuthorizedSession(_ context.Context, id string) (SessionIdentity, error) {
	r.calls++
	if r.revokeAfter > 0 && r.calls >= r.revokeAfter {
		return SessionIdentity{}, ErrDenied
	}
	return SessionIdentity{RuntimeSessionID: id, TenantID: "tenant", AgentID: "agent"}, nil
}

func TestWorkspaceBridgeRefusesMissingDurableReceiptStore(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("network reached without receipt ledger") })
	writer := &fakeWorkspaceWriter{}
	bridge := NewWorkspaceBridge(b, fixedSessionResolver{sessionID: "sess", allow: true}, writer, nil)
	if err := bridge.FetchToWorkspace(context.Background(), "sess", "receipt-1", "https://api.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing receipt ledger allowed egress: %v", err)
	}
	if writer.calls != 0 {
		t.Fatal("missing ledger wrote a receipt")
	}
}

func TestWorkspaceBridgeRejectsReplayedReceiptAcrossInstances(t *testing.T) {
	hits := 0
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { hits++; _, _ = w.Write([]byte("allowed")) })
	ledger := NewSQLReceiptStore(receiptTestDB(t))
	writer := &fakeWorkspaceWriter{}
	r := fixedSessionResolver{sessionID: "sess", allow: true}
	first := NewWorkspaceBridge(b, r, writer, ledger)
	second := NewWorkspaceBridge(b, r, writer, NewSQLReceiptStore(ledger.db))
	if err := first.FetchToWorkspace(context.Background(), "sess", "receipt-1", "https://api.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := second.FetchToWorkspace(context.Background(), "sess", "receipt-1", "https://api.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("replay was accepted: %v", err)
	}
	if hits != 1 || writer.calls != 1 {
		t.Fatalf("replay caused outbound requests/writes: hits=%d writes=%d", hits, writer.calls)
	}
}

func TestWorkspaceBridgeRechecksSessionAfterOutboundFetch(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("served")) })
	resolver := &mutableSessionResolver{revokeAfter: 2}
	writer := &fakeWorkspaceWriter{}
	bridge := NewWorkspaceBridge(b, resolver, writer, NewSQLReceiptStore(receiptTestDB(t)))
	err := bridge.FetchToWorkspace(context.Background(), "sess", "receipt-1", "https://api.example.test")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked session was delivered: %v", err)
	}
	if writer.calls != 0 || resolver.calls != 2 {
		t.Fatalf("expected 2 auth checks and 0 writes: %+v %+v", resolver, writer)
	}
}

func TestWorkspaceBridgeRejectsPolicyRevisionChangedWhileFetching(t *testing.T) {
	var broker *Broker
	broker, _ = newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if err := broker.policies.Apply(Snapshot{Revision: 2}); err != nil {
			t.Errorf("revoke: %v", err)
		}
		_, _ = w.Write([]byte("no-delivery"))
	})
	writer := &fakeWorkspaceWriter{}
	bridge := NewWorkspaceBridge(broker, fixedSessionResolver{sessionID: "sess", allow: true}, writer, NewSQLReceiptStore(receiptTestDB(t)))
	err := bridge.FetchToWorkspace(context.Background(), "sess", "receipt-1", "https://api.example.test")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("policy changed during request but delivered: %v", err)
	}
	if writer.calls != 0 {
		t.Fatal("revoked policy delivered response")
	}
}

func TestWorkspaceBridgeRejectsNilPolicyStoreWithoutReservingReceipt(t *testing.T) {
	writer := &fakeWorkspaceWriter{}
	db := receiptTestDB(t)
	bridge := NewWorkspaceBridge(&Broker{}, fixedSessionResolver{sessionID: "sess", allow: true}, writer, NewSQLReceiptStore(db))
	err := bridge.FetchToWorkspace(context.Background(), "sess", "receipt-1", "https://api.example.test")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("unconfigured broker should fail closed: %v", err)
	}
	var reserved int
	if err := db.QueryRow("SELECT COUNT(*) FROM agent_egress_receipts").Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 0 || writer.calls != 0 {
		t.Fatalf("unconfigured broker reserved or wrote receipt: %d %d", reserved, writer.calls)
	}
}
