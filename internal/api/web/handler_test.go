package web_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BonzTM/bloom/internal/api/web"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><title>Bloom</title>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":       {Data: []byte("<svg/>")},
	}
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandlerServesIndexForRootAndDeepLinks(t *testing.T) {
	t.Parallel()
	h := web.Handler(testFS(), slog.New(slog.DiscardHandler))
	for _, target := range []string{"/", "/about", "/users/42", "/assets/"} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", target, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>Bloom</title>") {
			t.Fatalf("%s: body %q is not index.html", target, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("%s: content-type %q", target, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: cache-control %q, want no-cache", target, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
			t.Fatalf("%s: csp %q lacks default-src 'self'", target, got)
		}
	}
}

func TestHandlerServesFingerprintedAssetsImmutable(t *testing.T) {
	t.Parallel()
	h := web.Handler(testFS(), nil)
	rec := get(t, h, http.MethodGet, "/assets/app-abc.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if rec.Body.String() != "console.log(1)" {
		t.Fatalf("body %q", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("cache-control %q, want immutable", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("asset carries document csp %q", got)
	}
}

func TestHandlerServesOtherFilesWithRevalidation(t *testing.T) {
	t.Parallel()
	h := web.Handler(testFS(), nil)
	rec := get(t, h, http.MethodGet, "/favicon.svg")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control %q, want no-cache", got)
	}
}

func TestHandlerRejectsNonReadMethods(t *testing.T) {
	t.Parallel()
	h := web.Handler(testFS(), nil)
	rec := get(t, h, http.MethodPost, "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("allow %q", got)
	}
}

func TestHandlerHeadOmitsBody(t *testing.T) {
	t.Parallel()
	h := web.Handler(testFS(), nil)
	rec := get(t, h, http.MethodHead, "/about")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD status %d body %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestHandlerWithoutIndexIs404NotPanic(t *testing.T) {
	t.Parallel()
	h := web.Handler(fstest.MapFS{}, nil)
	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestHandlerDoesNotEscapeRoot(t *testing.T) {
	t.Parallel()
	h := web.Handler(testFS(), nil)
	rec := get(t, h, http.MethodGet, "/../../etc/passwd")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>Bloom</title>") {
		t.Fatalf("traversal attempt: status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestDistIsEmbedded(t *testing.T) {
	t.Parallel()
	if _, err := web.Dist(); err != nil {
		t.Fatalf("Dist: %v", err)
	}
}
