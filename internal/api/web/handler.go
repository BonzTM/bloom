package web

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

// indexFile is the SPA entry document Vite emits.
const indexFile = "index.html"

// assetPrefix is where Vite puts fingerprinted files, which may be cached
// forever because their names change with their content.
const assetPrefix = "/assets/"

// documentCSP is the policy for HTML responses. It is stricter than a typical
// SPA default on purpose: scripts and styles are same-origin files, connect is
// same-origin (the JSON API), and images allow data: for inline placeholders.
// Loosen one directive at a time, with the reason next to it, when a feature
// needs it. Images also allow TMDB's image host, which serves the posters on
// the requests pages; no other third-party origin is allowed anywhere.
const documentCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: https://image.tmdb.org; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// Handler serves the SPA from fsys. Requests for existing files are served as
// static assets; any other GET or HEAD path receives index.html so client-side
// routing works on deep links. Other methods get 405. Callers mount it at "/"
// AFTER every API and probe route, so it never shadows them.
func Handler(fsys fs.FS, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := cleanPath(r.URL.Path)
		if name != indexFile && fileExists(fsys, name) {
			setAssetHeaders(w, r.URL.Path)
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, fsys, logger)
	})
}

// cleanPath maps a URL path to an fs.FS name: no leading slash, "." for root.
func cleanPath(p string) string {
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name == "" {
		return indexFile
	}
	return name
}

// fileExists reports whether name is a regular file in fsys. Directories are
// not files: a request for "/assets/" falls through to the SPA fallback rather
// than a directory listing.
func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// setAssetHeaders sets cache policy for static files. Fingerprinted assets are
// immutable; everything else (favicon, manifest, the worker) revalidates.
func setAssetHeaders(w http.ResponseWriter, urlPath string) {
	if strings.HasPrefix(urlPath, assetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// serveIndex writes index.html with the document headers. A missing index
// means the binary was built without a frontend; that is a 404 with a plain
// explanation, not a 500, because the API is still fully usable.
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS, logger *slog.Logger) {
	f, err := fsys.Open(indexFile)
	if err != nil {
		http.Error(w, "web UI not built into this binary", http.StatusNotFound)
		return
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			logger.WarnContext(r.Context(), "close index.html", "error", cerr)
		}
	}()
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Content-Security-Policy", documentCSP)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		logger.WarnContext(r.Context(), "write index.html", "error", err)
	}
}
