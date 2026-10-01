package webui

import (
	"embed"
	"io/fs"
)

// embedded contains both the committed fallback page and, in release builds,
// the Vite output written to dist/web before the Go binary is compiled.
//
//go:embed all:dist
var embedded embed.FS

func embeddedAssets() fs.FS {
	if built, err := fs.Sub(embedded, "dist/web"); err == nil {
		if _, err := fs.Stat(built, "index.html"); err == nil {
			return built
		}
	}

	fallback, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return fallback
}
