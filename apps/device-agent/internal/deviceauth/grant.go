//go:build linux

package deviceauth

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const GrantAudience = "dev-plane/device-tool-grant/v1"
const maxGrantLifetime = 2 * time.Minute

// Grant binds a remote tool authorization to one actor, organization, device,
// and workspace. It is not a substitute for the control plane's authorization
// and approval checks before issuing the grant.
type Grant struct {
	Audience       string   `json:"audience"`
	DeviceID       string   `json:"deviceId"`
	ActorID        string   `json:"actorId"`
	OrganizationID string   `json:"organizationId"`
	WorkspaceID    string   `json:"workspaceId"`
	Tools          []string `json:"tools"`
	IssuedAt       int64    `json:"issuedAt"`
	ExpiresAt      int64    `json:"expiresAt"`
}

type SignedGrant struct {
	Grant     Grant  `json:"grant"`
	Signature []byte `json:"signature"`
}

var readOnlyToolNames = map[string]bool{
	"workspaces_list": true,
	"files_list":      true,
	"files_read":      true,
	"files_search":    true,
	"jobs_start":      true,
	"jobs_status":     true,
	"jobs_cancel":     true,
}

func validateGrant(g Grant) error {
	if g.Audience != GrantAudience || strings.TrimSpace(g.DeviceID) == "" || strings.TrimSpace(g.ActorID) == "" || strings.TrimSpace(g.OrganizationID) == "" || strings.TrimSpace(g.WorkspaceID) == "" {
		return errors.New("missing or invalid grant audience or scope")
	}
	if g.ExpiresAt <= g.IssuedAt || g.ExpiresAt-g.IssuedAt > int64(maxGrantLifetime/time.Second) {
		return errors.New("invalid grant lifetime")
	}
	if len(g.Tools) == 0 || len(g.Tools) > len(readOnlyToolNames) {
		return errors.New("invalid grant tools")
	}
	for _, name := range g.Tools {
		if !readOnlyToolNames[name] {
			return fmt.Errorf("unapproved tool: %q", name)
		}
	}
	names := append([]string(nil), g.Tools...)
	slices.Sort(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			return errors.New("duplicate tools")
		}
	}
	return nil
}

func grantMessage(g Grant) ([]byte, error) {
	if err := validateGrant(g); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	return append([]byte("dev-plane/grant-signature/v1\n"), payload...), nil
}

// SignGrant is for the trusted control plane after user authorization. Never
// expose the signing key or a signing RPC to an untrusted device.
func SignGrant(g Grant, priv ed25519.PrivateKey) (SignedGrant, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return SignedGrant{}, errors.New("invalid issuer private key")
	}
	msg, err := grantMessage(g)
	if err != nil {
		return SignedGrant{}, err
	}
	return SignedGrant{Grant: g, Signature: ed25519.Sign(priv, msg)}, nil
}

// VerifyGrant checks issuer signature, clock bounds, and exact authorization
// scope. Call for every remote tool operation, not just at session creation.
func VerifyGrant(token SignedGrant, issuerPublic ed25519.PublicKey, now time.Time, deviceID, workspaceID, toolName string) error {
	if len(issuerPublic) != ed25519.PublicKeySize || len(token.Signature) != ed25519.SignatureSize {
		return errors.New("invalid grant signature")
	}
	if deviceID == "" || workspaceID == "" || toolName == "" || token.Grant.DeviceID != deviceID || token.Grant.WorkspaceID != workspaceID || !slices.Contains(token.Grant.Tools, toolName) {
		return errors.New("grant does not authorize the requested operation")
	}
	msg, err := grantMessage(token.Grant)
	if err != nil {
		return err
	}
	if !ed25519.Verify(issuerPublic, msg, token.Signature) {
		return errors.New("untrusted or modified grant")
	}
	nowSec := now.Unix()
	if nowSec < token.Grant.IssuedAt || nowSec >= token.Grant.ExpiresAt {
		return errors.New("grant expired or not yet valid")
	}
	return nil
}
