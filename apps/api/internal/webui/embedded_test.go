package webui

import (
	"testing"
	"testing/fstest"
)

func TestSelectBuiltAssetsRejectsMissingViteBuild(t *testing.T) {
	root := fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("fallback")},
	}

	if _, err := selectBuiltAssets(root); err == nil {
		t.Fatal("expected missing dist/web/index.html to fail")
	}
}

func TestSelectBuiltAssetsUsesViteOutput(t *testing.T) {
	root := fstest.MapFS{
		"dist/index.html":     &fstest.MapFile{Data: []byte("fallback")},
		"dist/web/index.html": &fstest.MapFile{Data: []byte("vite")},
	}

	assets, err := selectBuiltAssets(root)
	if err != nil {
		t.Fatalf("selectBuiltAssets() error: %v", err)
	}

	data, err := fstest.TestFS(assets, "index.html")
	if err != nil {
		t.Fatalf("selected assets do not expose index.html: %v", err)
	}
	_ = data
}
