package egresspolicy

import (
	"errors"
	"net/netip"
	"testing"
)

func publicIP() []netip.Addr { return []netip.Addr{netip.MustParseAddr("1.1.1.1")} }

func TestAuthorizeRequiresTrustedScopeExactHostAndPublicResolution(t *testing.T) {
	s := NewStore()
	if err := s.Apply(Snapshot{Revision: 1, Rules: []Rule{
		{TenantID: "tenant-a", AgentID: "agent-a", Hosts: []string{"api.openai.com"}},
		{TenantID: "tenant-b", AgentID: "agent-b", Hosts: []string{"api.github.com"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize("tenant-a", "agent-a", "https://api.openai.com/v1/chat/completions", publicIP()); err != nil {
		t.Fatalf("expected authorization: %v", err)
	}
	denied := []struct {
		tenant, agent, target string
		ips                   []netip.Addr
	}{
		{"tenant-b", "agent-a", "https://api.openai.com", publicIP()},
		{"tenant-a", "agent-b", "https://api.openai.com", publicIP()},
		{"tenant-a", "agent-a", "https://evil.api.openai.com", publicIP()},
		{"tenant-a", "agent-a", "http://api.openai.com", publicIP()},
		{"tenant-a", "agent-a", "https://api.openai.com:8443", publicIP()},
		{"tenant-a", "agent-a", "https://user@api.openai.com", publicIP()},
		{"tenant-a", "agent-a", "https://api.openai.com", nil},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("10.1.2.3")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("64:ff9b::a9fe:a9fe")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("2002:0a01:0203::1")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("2001::1")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}},
		{"tenant-a", "agent-a", "https://api.openai.com", []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("169.254.169.254")}},
	}
	for _, c := range denied {
		if err := s.Authorize(c.tenant, c.agent, c.target, c.ips); !errors.Is(err, ErrDenied) {
			t.Errorf("expected deny for %q / %q: %v", c.tenant, c.target, err)
		}
	}
}

func TestApplyRejectsInvalidAndStaleSnapshotsWithoutLosingPolicy(t *testing.T) {
	s := NewStore()
	valid := Snapshot{Revision: 3, Rules: []Rule{{TenantID: "a", AgentID: "one", Hosts: []string{"github.com"}}}}
	if err := s.Apply(valid); err != nil {
		t.Fatal(err)
	}
	invalid := []Snapshot{
		{Revision: 3, Rules: []Rule{{TenantID: "a", AgentID: "one", Hosts: []string{"google.com"}}}},
		{Revision: 4, Rules: []Rule{{TenantID: "a", AgentID: "one", Hosts: []string{"*.github.com"}}}},
		{Revision: 4, Rules: []Rule{{TenantID: "a", AgentID: "one", Hosts: []string{"127.0.0.1"}}}},
		{Revision: 4, Rules: []Rule{{TenantID: "a", AgentID: "one", Hosts: []string{"https://github.com"}}}},
		{Revision: 4, Rules: []Rule{{TenantID: "a", AgentID: "one", Hosts: []string{"github.com"}}, {TenantID: "a", AgentID: "one", Hosts: []string{"github.com"}}}},
		{Revision: 4, Rules: []Rule{{TenantID: "", AgentID: "one", Hosts: []string{"github.com"}}}},
	}
	for _, snap := range invalid {
		if err := s.Apply(snap); err == nil {
			t.Errorf("expected rejection for %+v", snap)
		}
		if got := s.Revision(); got != 3 {
			t.Errorf("revision changed after invalid snapshot: %d", got)
		}
		if err := s.Authorize("a", "one", "https://github.com", publicIP()); err != nil {
			t.Errorf("previous policy lost: %v", err)
		}
	}
	valid.Rules[0].Hosts[0] = "evil.com"
	if err := s.Authorize("a", "one", "https://github.com", publicIP()); err != nil {
		t.Fatalf("caller mutated active policy: %v", err)
	}
	if err := s.Apply(Snapshot{Revision: 4, Rules: nil}); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize("a", "one", "https://github.com", publicIP()); !errors.Is(err, ErrDenied) {
		t.Fatalf("revocation failed: %v", err)
	}
}
