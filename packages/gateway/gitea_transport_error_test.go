package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type giteaErrorRoundTripper func(*http.Request) (*http.Response, error)

func (f giteaErrorRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type giteaSyntheticTimeout struct{}

func (giteaSyntheticTimeout) Error() string   { return "transport echoed synthetic-secret" }
func (giteaSyntheticTimeout) Timeout() bool   { return true }
func (giteaSyntheticTimeout) Temporary() bool { return true }

func TestGiteaForgeTransportErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		want    error
		timeout bool
	}{
		{name: "provider-controlled failure", err: fmt.Errorf("transport echoed synthetic-secret")},
		{name: "canceled", err: fmt.Errorf("transport echoed synthetic-secret: %w", context.Canceled), want: context.Canceled},
		{name: "deadline", err: fmt.Errorf("transport echoed synthetic-secret: %w", context.DeadlineExceeded), want: context.DeadlineExceeded, timeout: true},
		{name: "network timeout", err: giteaSyntheticTimeout{}, want: context.DeadlineExceeded, timeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: giteaErrorRoundTripper(func(*http.Request) (*http.Response, error) { return nil, tc.err })}
			forge, err := NewGiteaForge("https://forge.example", client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = forge.GetRepository(context.Background(), ForgeCredential{AccessToken: "synthetic-secret"}, "acme", "widget")
			if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("transport error was absent or retained secret text")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want classification %v, got %v", tc.want, err)
			}
			if tc.timeout {
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("timeout not classifiable: %v", err)
				}
			}
		})
	}
}

func TestGiteaForgeClientDeadlineIsSanitized(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Millisecond, Transport: giteaErrorRoundTripper(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, fmt.Errorf("transport echoed synthetic-secret: %w", r.Context().Err())
	})}
	forge, err := NewGiteaForge("https://forge.example", client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = forge.GetRepository(context.Background(), ForgeCredential{AccessToken: "synthetic-secret"}, "acme", "widget")
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("client timeout was not sanitized or deadline classification was lost")
	}
}
