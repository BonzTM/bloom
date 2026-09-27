package jellyfin

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

const testItemImageID = "0123456789abcdef0123456789abcdef"

func TestItemImageBuildsServerOwnedRequestAndReturnsMetadata(t *testing.T) {
	observer := &recordingObserver{}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/Items/"+testItemImageID+"/Images/Backdrop" ||
			r.URL.Query().Get("maxWidth") != "720" || r.URL.Query().Get("quality") != "90" {
			t.Errorf("image target = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("If-None-Match") != `"old"` || !strings.Contains(r.Header.Get("Authorization"), "test-api-key") {
			t.Errorf("request headers = %#v", r.Header)
		}
		return imageResponse(http.StatusOK, "image/webp", `"new"`, "image-data"), nil
	})
	client := newTransportClient(t, transport)
	client.observer = observer
	image, err := client.ItemImage(t.Context(), testItemImageID, core.ItemImageBackdrop, 720, `"old"`)
	if err != nil || image.ContentType != "image/webp" || image.ETag != `"new"` ||
		image.NotModified || !bytes.Equal(image.Body, []byte("image-data")) {
		t.Fatalf("ItemImage = %+v, %v", image, err)
	}
	if len(observer.requests) != 1 || observer.requests[0] != (metricObservation{"item_image", "success"}) {
		t.Fatalf("request metrics = %+v", observer.requests)
	}
}

func TestItemImageHonorsNotModified(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("If-None-Match") != `W/"cached"` {
			t.Errorf("If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		return imageResponse(http.StatusNotModified, "", `W/"cached"`, ""), nil
	})
	image, err := newTransportClient(t, transport).ItemImage(
		t.Context(), testItemImageID, core.ItemImagePrimary, 400, `W/"cached"`,
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
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return imageResponse(testCase.status, testCase.contentType, "", testCase.body), nil
			})
			_, err := newTransportClient(t, transport).ItemImage(t.Context(), testItemImageID, core.ItemImageThumb, 64, "")
			assertMediaError(t, err, testCase.kind)
		})
	}
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return imageResponse(http.StatusNotFound, "", "", ""), nil
	}))
	for _, call := range []func() error{
		func() error {
			_, err := client.ItemImage(t.Context(), "bad\nitem", core.ItemImagePrimary, 400, "")
			return err
		},
		func() error { _, err := client.ItemImage(t.Context(), testItemImageID, "Poster", 400, ""); return err },
		func() error {
			_, err := client.ItemImage(t.Context(), testItemImageID, core.ItemImagePrimary, 63, "")
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatal("invalid image request succeeded")
		}
	}
}

func TestItemImageRejectsInvalidItemID(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("invalid item id reached upstream")
		return nil, errors.New("unexpected upstream request")
	}))
	invalid := []string{".", "..", "a/b", "a%2Fb", `a\b`, "%2e%2e", "", strings.Repeat("a", 33)}
	for _, itemID := range invalid {
		_, err := client.ItemImage(t.Context(), itemID, core.ItemImagePrimary, 400, "")
		if !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("ItemImage(%q) error = %v, want ErrInvalidArgument", itemID, err)
		}
	}
}

func imageResponse(status int, contentType, etag, body string) *http.Response {
	header := make(http.Header, 2)
	header.Set("Content-Type", contentType)
	header.Set("ETag", etag)
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
