// Package egresspolicy defines fail-closed, revisioned destination policies for
// agent network gateways. Only trusted scheduler identities may select a rule.
// This package does not open connections: the caller MUST pin approved DNS
// addresses in its transport, reauthorize every redirect, and prohibit direct
// network bypass from agent sandboxes.
package egresspolicy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
)

var ErrDenied = errors.New("agent egress denied")

// Rule is a tenant/agent-scoped set of exact HTTPS hostnames.
type Rule struct {
	TenantID string
	AgentID  string
	Hosts    []string
}

// Snapshot is published by the trusted control plane. Revisions are monotonically increasing.
type Snapshot struct {
	Revision uint64
	Rules    []Rule
}

type key struct{ tenant, agent string }

// Store retains the previous approved snapshot until a complete replacement validates.
type Store struct {
	mu       sync.RWMutex
	revision uint64
	rules    map[key]map[string]struct{}
}

func NewStore() *Store { return &Store{rules: make(map[key]map[string]struct{})} }

func (s *Store) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

// Apply validates a complete replacement before atomically publishing it.
// A valid empty ruleset intentionally revokes all previously permitted hosts.
func (s *Store) Apply(snapshot Snapshot) error {
	if snapshot.Revision == 0 {
		return errors.New("egress snapshot revision must be positive")
	}
	if len(snapshot.Rules) > 10000 {
		return errors.New("egress snapshot exceeds rule limit")
	}
	next := make(map[key]map[string]struct{}, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		if strings.TrimSpace(rule.TenantID) != rule.TenantID || rule.TenantID == "" ||
			strings.TrimSpace(rule.AgentID) != rule.AgentID || rule.AgentID == "" {
			return errors.New("egress policy requires nonempty normalized tenant and agent IDs")
		}
		k := key{rule.TenantID, rule.AgentID}
		if _, ok := next[k]; ok {
			return fmt.Errorf("duplicate egress policy scope %q / %q", k.tenant, k.agent)
		}
		if len(rule.Hosts) == 0 || len(rule.Hosts) > 512 {
			return fmt.Errorf("invalid hostname count for %q / %q", k.tenant, k.agent)
		}
		hosts := make(map[string]struct{}, len(rule.Hosts))
		for _, host := range rule.Hosts {
			if !validHostname(host) || strings.ToLower(host) != host {
				return fmt.Errorf("invalid egress hostname %q", host)
			}
			if _, exists := hosts[host]; exists {
				return fmt.Errorf("duplicate egress hostname %q", host)
			}
			hosts[host] = struct{}{}
		}
		next[k] = hosts
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.Revision <= s.revision {
		return fmt.Errorf("stale egress snapshot: %d <= %d", snapshot.Revision, s.revision)
	}
	s.rules = next
	s.revision = snapshot.Revision
	return nil
}

// Authorize checks an exact HTTPS destination against a trusted tenant/agent
// scope and the *entire* resolved address set. The gateway MUST separately
// enforce the approved IPs at connect time; checking DNS alone is insufficient.
func (s *Store) Authorize(tenantID, agentID, destination string, resolved []netip.Addr) error {
	parsed, err := url.Parse(destination)
	if err != nil || parsed == nil || parsed.Scheme != "https" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" {
		return ErrDenied
	}
	host := parsed.Hostname()
	if !validHostname(host) || host != strings.ToLower(host) {
		return ErrDenied
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return ErrDenied
	}
	if len(resolved) == 0 {
		return ErrDenied
	}
	for _, addr := range resolved {
		if !isAllowedPublicIP(addr) {
			return ErrDenied
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.rules[key{tenantID, agentID}][host]; !ok {
		return ErrDenied
	}
	return nil
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil || strings.ContainsAny(host, "/:@ %\\\t\n\r") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 IPv4 embedding
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use translation
	netip.MustParsePrefix("2001::/32"),      // Teredo IPv4 tunneling
	netip.MustParsePrefix("2002::/16"),      // 6to4 IPv4 embedding
	netip.MustParsePrefix("2001:db8::/32"),
}

func isAllowedPublicIP(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range nonPublicRanges {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
