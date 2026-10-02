package webui

import (
	"embed"
	"fmt"
	"io/fs"
)

// embedded contains the Vite output written to dist/web before the Go binary
// is compiled. The committed dist/index.html placeholder is intentionally not
// accepted by production startup.
//
//go:embed all:dist
var embedded embed.FS

func embeddedAssets() (fs.FS, error) {
	return selectBuiltAssets(embedded)
}

func selectBuiltAssets(root fs.FS) (fs.FS, error) {
	built, err := fs.Sub(root, "dist/web")
	if err != nil {
		return nil, fmt.Errorf("open embedded Vite assets: %w", err)
	}
	if _, err := fs.Stat(built, "index.html"); err != nil {
		return nil, fmt.Errorf("Vite UI is not built: missing dist/web/index.html: %w", err)
	}
	return built, nil
}
