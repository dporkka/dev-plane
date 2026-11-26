package config

import (
	"reflect"
	"testing"
)

func TestBrowserAllowedOriginsConfiguration(t *testing.T) {
	t.Setenv("BROWSER_ALLOWED_ORIGINS", "")
	origins := BrowserAllowedOrigins()
	if len(origins) != 0 {
		t.Fatalf("empty browser origin config enabled browser: %#v", origins)
	}
	t.Setenv("BROWSER_ALLOWED_ORIGINS", " https://a.example ,https://b.example ")
	origins = BrowserAllowedOrigins()
	if !reflect.DeepEqual(origins, []string{"https://a.example", "https://b.example"}) {
		t.Fatalf("unexpected origins: %#v", origins)
	}
}
