package handlers

import (
	"strings"
	"testing"
)

func TestVerifiedGitHubPrimaryEmail(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		want      string
		wantError bool
	}{
		{name: "verified primary", response: `[{"email":"a@example.com","primary":true,"verified":true},{"email":"other@example.com","primary":false,"verified":true}]`, want: "a@example.com"},
		{name: "unverified primary", response: `[{"email":"a@example.com","primary":true,"verified":false}]`, want: ""},
		{name: "verified non-primary", response: `[{"email":"a@example.com","primary":false,"verified":true}]`, want: ""},
		{name: "malformed json", response: `{"email":`, wantError: true},
		{name: "wrong payload", response: `{"email":"example@example.com"}`, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verifiedGitHubPrimaryEmail(strings.NewReader(tt.response))
			if (err != nil) != tt.wantError {
				t.Fatalf("err=%v wantError=%v", err, tt.wantError)
			}
			if got != tt.want {
				t.Fatalf("email=%q want=%q", got, tt.want)
			}
		})
	}
}
