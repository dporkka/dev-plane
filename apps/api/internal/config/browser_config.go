package config

import (
	"os"
	"strings"
)

// BrowserAllowedOrigins reads the independent, operator-supplied browser
// destination allowlist. Empty configuration disables new job submissions.
func BrowserAllowedOrigins() []string {
	raw := os.Getenv("BROWSER_ALLOWED_ORIGINS")
	var origins []string
	for _, part := range strings.Split(raw, ",") {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}
