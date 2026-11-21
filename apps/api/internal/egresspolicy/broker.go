package egresspolicy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrResponseTooLarge = errors.New("egress response exceeds size limit")

const (
	defaultResponseLimit int64 = 1 << 20
	maxURLBytes                = 4096
	maxRedirects               = 3
)

// Broker executes bounded outbound GETs in the trusted host process, rather
// than inside Docker's --network=none sandbox. It never accepts arbitrary
// caller-provided headers or credentials and does not create an HTTP listener.
// The trusted scheduler must derive tenant/agent identities from its session.
type Broker struct {
	policies         *Store
	resolve          func(context.Context, string) ([]netip.Addr, error)
	dial             func(context.Context, string, string) (net.Conn, error)
	roots            *x509.CertPool // nil uses the operating system trust store
	maxResponseBytes int64
}

type FetchResult struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

// NewBroker constructs the privileged *host-side* outbound request helper.
// Its consumer is responsible for access control on invocation and preventing
// agents from bypassing the broker through networking or shell escape.
func NewBroker(policies *Store) *Broker {
	return &Broker{
		policies:         policies,
		maxResponseBytes: defaultResponseLimit,
		resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
	}
}

// Fetch follows a maximum of three redirects; every hop is resolved, checked,
// and pinned independently. No Authorization headers or cookies are forwarded.
func (b *Broker) Fetch(ctx context.Context, tenantID, agentID, target string) (*FetchResult, error) {
	if b == nil || b.policies == nil || ctx == nil {
		return nil, ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for redirects := 0; redirects <= maxRedirects; redirects++ {
		res, next, err := b.fetchOnce(ctx, tenantID, agentID, target)
		if err != nil {
			return nil, err
		}
		if next == "" {
			return res, nil
		}
		if redirects == maxRedirects {
			return nil, fmt.Errorf("%w: redirect limit", ErrDenied)
		}
		target = next
	}
	return nil, ErrDenied
}

func (b *Broker) fetchOnce(ctx context.Context, tenantID, agentID, target string) (*FetchResult, string, error) {
	if len(target) == 0 || len(target) > maxURLBytes {
		return nil, "", ErrDenied
	}
	u, err := url.Parse(target)
	if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, "", ErrDenied
	}
	host := u.Hostname()
	if b.resolve == nil {
		return nil, "", ErrDenied
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	addrs, err := b.resolve(resolveCtx, host)
	cancel()
	if err != nil || b.policies.Authorize(tenantID, agentID, target, addrs) != nil {
		return nil, "", ErrDenied
	}
	// Never use environment HTTP(S)_PROXY values, even if present on the host.
	// DialContext must use only one of the previously approved DNS results.
	dial := b.dial
	if dial == nil {
		dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: -1}
		dial = dialer.DialContext
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			destHost, port, e := net.SplitHostPort(address)
			if e != nil || destHost != host || port != "443" || (network != "tcp" && network != "tcp4" && network != "tcp6") {
				return nil, ErrDenied
			}
			// Recheck the *current* policy immediately before connecting, so
			// revocations issued after DNS resolution are enforced where possible.
			if e := b.policies.Authorize(tenantID, agentID, target, addrs); e != nil {
				return nil, e
			}
			var lastErr error
			for _, addr := range addrs {
				// No second DNS lookup: the kernel sees a numeric address only.
				conn, e := dial(ctx, "tcp", net.JoinHostPort(addr.String(), "443"))
				if e == nil {
					return conn, nil
				}
				lastErr = e
			}
			return nil, lastErr
		},
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: b.roots},
		TLSHandshakeTimeout:    4 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", ErrDenied
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("egress request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		if location != "" {
			parsed, e := url.Parse(location)
			if e != nil {
				return nil, "", ErrDenied
			}
			next := u.ResolveReference(parsed)
			// The next iteration revalidates the redirect target and IP set.
			if next.User != nil || next.Fragment != "" || !strings.EqualFold(next.Scheme, "https") {
				return nil, "", ErrDenied
			}
			return nil, next.String(), nil
		}
	}
	limit := b.maxResponseBytes
	if limit <= 0 || limit > defaultResponseLimit {
		limit = defaultResponseLimit
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", ErrResponseTooLarge
	}
	return &FetchResult{StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: data}, "", nil
}
