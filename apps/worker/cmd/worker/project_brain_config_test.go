package main

import "testing"

func TestEnvBoolOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback bool
		want     bool
	}{
		{name: "unset false", fallback: false, want: false},
		{name: "unset true", fallback: true, want: true},
		{name: "true", value: "true", fallback: false, want: true},
		{name: "one", value: "1", fallback: false, want: true},
		{name: "false", value: "false", fallback: true, want: false},
		{name: "invalid uses fallback", value: "not-a-bool", fallback: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "PROJECT_BRAIN_TEST_BOOL"
			if tt.value == "" {
				t.Setenv(key, "")
			} else {
				t.Setenv(key, tt.value)
			}
			if got := envBoolOrDefault(key, tt.fallback); got != tt.want {
				t.Fatalf("envBoolOrDefault(%q, %v) = %v, want %v", key, tt.fallback, got, tt.want)
			}
		})
	}
}
