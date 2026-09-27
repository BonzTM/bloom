package jellyfin

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestItemImageBuildsServerOwnedRequestAndReturnsMetadata(t *testing.T) {
	observer := &recordingObserver{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/item-1/Images/Backdrop" ||
			r.URL.Query().Get("maxWidth") != "720" || r.URL.Query().Get("quality") != "90" {
			t.Errorf("image target = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("If-None-Match") != `"old"` || !strings.Contains(r.Header.Get("Authorization"), "test-api-key") {
			t.Errorf("request headers = %#v", r.Header)
		}
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("ETag", `"new"`)
		if _, err := w.Write([]byte("image-data")); err != nil {
			t.Errorf("write image response: %v", err)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func(cfg *Config) { cfg.Observer = observer })
	image, err := client.ItemImage(t.Context(), "item-1", core.ItemImageBackdrop, 720, `"old"`)
	if err != nil || image.ContentType != "image/webp" || image.ETag != `"new"` ||
		image.NotModified || !bytes.Equal(image.Body, []byte("image-data")) {
		t.Fatalf("ItemImage = %+v, %v", image, err)
	}
	if len(observer.requests) != 1 || observer.requests[0] != (metricObservation{"item_image", "success"}) {
		t.Fatalf("request metrics = %+v", observer.requests)
	}
}

func TestItemImageHonorsNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `W/"cached"` {
			t.Errorf("If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", `W/"cached"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	image, err := newTestClient(t, server, nil).ItemImage(
		t.Context(), "item-1", core.ItemImagePrimary, 400, `W/"cached"`,
	)
	if err != nil || !image.NotModified || image.ETag != `W/"cached"` || len(image.Body) != 0 {
		t.Fatalf("ItemImage = %+v, %v", image, err)
	}
}

func TestItemImageRejectsUnsafeInputsAndResponses(t *testing.T) {
	tests := []struct {
		name, contentType string
		status            int
		body              string
		kind              core.MediaServerErrorKind
	}{
		{name: "missing", status: http.StatusNotFound, kind: core.MediaServerNotFound},
		{name: "content type", status: http.StatusOK, contentType: "image/svg+xml", body: "<svg/>", kind: core.MediaServerMalformed},
		{name: "oversized", status: http.StatusOK, contentType: "image/jpeg", body: strings.Repeat("x", maxImageResponseBytes+1), kind: core.MediaServerMalformed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", testCase.contentType)
				w.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(w, testCase.body)
			}))
			_, err := newTestClient(t, server, nil).ItemImage(t.Context(), "item-1", core.ItemImageThumb, 64, "")
			server.Close()
			assertMediaError(t, err, testCase.kind)
		})
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client := newTestClient(t, server, nil)
	for _, call := range []func() error{
		func() error {
			_, err := client.ItemImage(t.Context(), "bad\nitem", core.ItemImagePrimary, 400, "")
			return err
		},
		func() error { _, err := client.ItemImage(t.Context(), "item", "Poster", 400, ""); return err },
		func() error {
			_, err := client.ItemImage(t.Context(), "item", core.ItemImagePrimary, 63, "")
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatal("invalid image request succeeded")
		}
	}
}
