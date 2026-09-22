// Package web serves the single-page application that web/ builds and the Go
// binary embeds, per ADR 0002. It owns only the HTML boundary: static asset
// delivery, the index.html fallback for client-side routes, and the document
// security headers. Everything the SPA needs from the backend goes through the
// JSON API in internal/api/http.
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// distFS holds the Vite build output. The web/ toolchain writes into this
// directory (see web/vite.config.ts); it is git-ignored except for .gitkeep,
// which keeps the embed pattern valid on a checkout with no frontend build.
//
//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded build output rooted at the dist directory.
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: sub dist: %w", err)
	}
	return sub, nil
}
