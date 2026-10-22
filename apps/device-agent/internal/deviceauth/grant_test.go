package deviceauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestScopedGrantRequiresValidGatewaySignatureAndExactScope(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	gatewayPub, gatewayPriv, _ := ed25519.GenerateKey(rand.Reader)
	device, _ := GenerateIdentity()
	grant := Grant{Audience: GrantAudience, DeviceID: device.ID, ActorID: "user-1", OrganizationID: "org-1", WorkspaceID: "repo-a", Tools: []string{"files_list", "files_read"}, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
	token, err := SignGrant(grant, gatewayPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyGrant(token, gatewayPub, now, device.ID, "repo-a", "files_read"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ device, workspace, tool string }{{"wrong", "repo-a", "files_read"}, {device.ID, "repo-b", "files_read"}, {device.ID, "repo-a", "jobs_start"}} {
		if err := VerifyGrant(token, gatewayPub, now, tc.device, tc.workspace, tc.tool); err == nil {
			t.Fatalf("accepted mismatched grant %+v", tc)
		}
	}
	token.Grant.WorkspaceID = "repo-b"
	if err := VerifyGrant(token, gatewayPub, now, device.ID, "repo-b", "files_read"); err == nil {
		t.Fatal("accepted tampered payload")
	}
}
func TestGrantRejectsExpiredOverbroadAndUnknownTools(t *testing.T) {
	now := time.Now().UTC()
	gatewayPub, gatewayPriv, _ := ed25519.GenerateKey(rand.Reader)
	device, _ := GenerateIdentity()
	base := Grant{Audience: GrantAudience, DeviceID: device.ID, ActorID: "user-1", OrganizationID: "org-1", WorkspaceID: "repo-a", Tools: []string{"files_read"}, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
	tok, _ := SignGrant(base, gatewayPriv)
	if err := VerifyGrant(tok, gatewayPub, now.Add(3*time.Minute), device.ID, "repo-a", "files_read"); err == nil {
		t.Fatal("accepted expired grant")
	}
	for _, tools := range [][]string{{"*"}, {"command_execute"}, {"files_read", "files_read"}, {}} {
		grant := base
		grant.Tools = tools
		if _, err := SignGrant(grant, gatewayPriv); err == nil {
			t.Fatalf("accepted tool names %v", tools)
		}
	}
	grant := base
	grant.ExpiresAt = now.Add(20 * time.Minute).Unix()
	if _, err := SignGrant(grant, gatewayPriv); err == nil {
		t.Fatal("accepted lengthy grant")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyGrant(tok, otherPub, now, device.ID, "repo-a", "files_read"); err == nil {
		t.Fatal("accepted untrusted gateway")
	}
}
