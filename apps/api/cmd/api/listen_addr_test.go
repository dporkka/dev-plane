package main

import (
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

func TestListenAddress(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"default preserves all interfaces", config.Config{Port: "8080"}, ":8080"},
		{"staging restricts to loopback", config.Config{Port: "56371", BindHost: "127.0.0.1"}, "127.0.0.1:56371"},
		{"ipv6 loopback", config.Config{Port: "8080", BindHost: "::1"}, "[::1]:8080"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenAddress(&tt.cfg); got != tt.want {
				t.Fatalf("listenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
