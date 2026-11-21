package egresspolicy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func newBrokerFixture(t *testing.T, handler http.HandlerFunc) (*Broker, *httptest.Server) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "api.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"api.example.test", "alt.example.test"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, public, private)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	root := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	root.AddCert(parsed)
	s := NewStore()
	if err := s.Apply(Snapshot{Revision: 1, Rules: []Rule{{TenantID: "tenant", AgentID: "agent", Hosts: []string{"api.example.test"}}}}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(s)
	b.roots = root
	b.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	b.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "1.1.1.1:443" {
			t.Errorf("dial not pinned to approved DNS: %q", addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, server.Listener.Addr().String())
	}
	return b, server
}

func TestBrokerFetchPinsDNSAndVerifiesTLSHostname(t *testing.T) {
	var requests int
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Host != "api.example.test" {
			t.Errorf("wrong origin Host: %s", r.Host)
		}
		if r.TLS == nil || r.TLS.ServerName != "api.example.test" {
			t.Errorf("TLS SNI not original hostname")
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("secure response"))
	})
	result, err := b.Fetch(context.Background(), "tenant", "agent", "https://api.example.test/data")
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 200 || string(result.Body) != "secure response" || requests != 1 {
		t.Fatalf("unexpected response: %+v, requests=%d", result, requests)
	}
}

func TestBrokerRejectsRedirectToUnauthorizedOrigin(t *testing.T) {
	var requests int
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "https://alt.example.test/secret", http.StatusFound)
	})
	_, err := b.Fetch(context.Background(), "tenant", "agent", "https://api.example.test/start")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("wanted fail-closed redirect: %v", err)
	}
	if requests != 1 {
		t.Fatalf("redirect bypass sent %d requests", requests)
	}
}

func TestBrokerRejectsDNSRebindingBeforeDial(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not reach network") })
	dialed := false
	b.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	b.dial = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}
	_, err := b.Fetch(context.Background(), "tenant", "agent", "https://api.example.test")
	if !errors.Is(err, ErrDenied) || dialed {
		t.Fatalf("DNS protection failed: err=%v dialed=%t", err, dialed)
	}
}

func TestBrokerLimitsResponseBody(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("A", 128))) })
	b.maxResponseBytes = 32
	_, err := b.Fetch(context.Background(), "tenant", "agent", "https://api.example.test")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected limit: %v", err)
	}
}

func TestBrokerNeverDisablesTLSVerification(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("bad")) })
	b.roots = nil // system trust roots must not accept self-signed fixture
	_, err := b.Fetch(context.Background(), "tenant", "agent", "https://api.example.test")
	if err == nil {
		t.Fatal("TLS verification must be enabled")
	}
}

func TestBrokerRequiresAuthorizedAgent(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected outbound request") })
	_, err := b.Fetch(context.Background(), "tenant", "spoofed-agent", "https://api.example.test")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("unexpected authorization: %v", err)
	}
}