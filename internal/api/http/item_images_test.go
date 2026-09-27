package http

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	itemImageID      = "0123456789abcdef0123456789abcdef"
	itemImagePath    = "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/items/" + itemImageID + "/image"
	itemImagePattern = `^(?:[0-9A-Fa-f]{32}|[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})$`
)

func TestItemImageEndpoint(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.mediaServers.image = core.ItemImage{
		Body: []byte("jpeg-data"), ContentType: "image/jpeg", ETag: `"image-etag"`,
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	req := httptest.NewRequest(http.MethodGet,
		"https://bloom.test"+itemImagePath+"?type=Primary&max_width=640", nil)
	req.AddCookie(cookie)
	req.Header.Set("If-None-Match", `"old-etag"`)
	request := httptest.NewRecorder()
	h.h.ServeHTTP(request, req)
	if request.Code != http.StatusOK || request.Body.String() != "jpeg-data" {
		t.Fatalf("image = %d %q", request.Code, request.Body.String())
	}
	if request.Header().Get("Content-Type") != "image/jpeg" || request.Header().Get("ETag") != `"image-etag"` {
		t.Fatalf("image headers = %v", request.Header())
	}
	if request.Header().Get("Cache-Control") != itemImageCacheControl {
		t.Fatalf("Cache-Control = %q", request.Header().Get("Cache-Control"))
	}
	if h.mediaServers.lastItemID != itemImageID || h.mediaServers.lastImageType != core.ItemImagePrimary ||
		h.mediaServers.lastMaxWidth != 640 || h.mediaServers.lastIfNoneMatch != `"old-etag"` || !h.mediaServers.deadlineSeen {
		t.Fatalf("image call = %+v", h.mediaServers)
	}
}

func TestItemImageDefaultsWidthAndHonorsNotModified(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.mediaServers.image = core.ItemImage{ETag: `"image-etag"`, NotModified: true}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	request := h.request(t, http.MethodGet, itemImagePath+"?type=Backdrop", "", cookie)
	if request.Code != http.StatusNotModified || request.Body.Len() != 0 {
		t.Fatalf("not modified = %d %q", request.Code, request.Body.String())
	}
	if h.mediaServers.lastMaxWidth != defaultItemImageWidth || request.Header().Get("ETag") != `"image-etag"` ||
		request.Header().Get("Cache-Control") != itemImageCacheControl {
		t.Fatalf("not modified headers = %v, width = %d", request.Header(), h.mediaServers.lastMaxWidth)
	}
}

func TestItemImageAcceptsEachReadPermission(t *testing.T) {
	for _, permission := range []core.Permission{
		core.PermissionStatsReadAll, core.PermissionStatsReadOwn, core.PermissionRequestsReadOwn,
	} {
		t.Run(string(permission), func(t *testing.T) {
			h := newAuthHarness(t, nil)
			h.mediaServers.image = core.ItemImage{Body: []byte("png"), ContentType: "image/png"}
			accountID := h.store.accounts["alice"].ID
			h.authorization.permissions[accountID] = []core.Permission{permission}
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			request := h.request(t, http.MethodGet, itemImagePath+"?type=Thumb", "", cookie)
			if request.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", request.Code, request.Body.String())
			}
		})
	}
}

func TestItemImageRejectsInvalidInput(t *testing.T) {
	tests := []string{
		"/api/v1/media-servers/not-a-uuid/items/" + itemImageID + "/image?type=Primary",
		"/api/v1/media-servers/33333333-3333-4333-8333-333333333333/items/%01/image?type=Primary",
		itemImagePath,
		itemImagePath + "?type=Poster",
		itemImagePath + "?type=Primary&type=Thumb",
		itemImagePath + "?type=Primary&max_width=63",
		itemImagePath + "?type=Primary&max_width=1281",
		itemImagePath + "?type=Primary&max_width=nope",
		itemImagePath + "?type=Primary&max_width=400&max_width=500",
		itemImagePath + "?type=Primary&max_width=%zz",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			request := h.request(t, http.MethodGet, target, "", cookie)
			if request.Code != http.StatusUnprocessableEntity || h.mediaServers.imageCalls != 0 {
				t.Fatalf("response = %d %s, calls = %d", request.Code, request.Body.String(), h.mediaServers.imageCalls)
			}
		})
	}
}

func TestItemImageRejectsInvalidItemID(t *testing.T) {
	invalid := []string{".", "..", "a/b", "a%2Fb", `a\b`, "%2e%2e", "", strings.Repeat("a", 33)}
	for _, itemID := range invalid {
		t.Run(itemID, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			req := httptest.NewRequest(http.MethodGet, "https://bloom.test/image?type=Primary", nil)
			req.SetPathValue("id", "33333333-3333-4333-8333-333333333333")
			req.SetPathValue("item_id", itemID)
			response := httptest.NewRecorder()
			h.server.handleItemImage(response, req)
			envelope := decodeEnvelope(t, response)
			if response.Code != http.StatusUnprocessableEntity || h.mediaServers.imageCalls != 0 {
				t.Fatalf("response = %d %s, calls = %d", response.Code, response.Body.String(), h.mediaServers.imageCalls)
			}
			if len(envelope.Fields) != 1 || envelope.Fields[0].Field != "item_id" {
				t.Fatalf("fields = %+v, want item_id", envelope.Fields)
			}
		})
	}
}

func TestItemImageMapsFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		want   int
		reason string
	}{
		{name: "missing", err: core.ErrNotFound, want: http.StatusNotFound},
		{name: "upstream", err: &core.MediaServerError{Kind: core.MediaServerMalformed}, want: http.StatusBadGateway, reason: reasonMalformed},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			h.mediaServers.err = test.err
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			request := h.request(t, http.MethodGet, itemImagePath+"?type=Primary", "", cookie)
			if request.Code != test.want {
				t.Fatalf("status = %d: %s", request.Code, request.Body.String())
			}
			if reason := decodeEnvelope(t, request).Reason; reason != test.reason {
				t.Fatalf("reason = %q, want %q", reason, test.reason)
			}
		})
	}
}

func TestItemImageMapsMissingCapabilityToNotFound(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.mediaServers.err = core.ErrNotFound
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	response := h.request(t, http.MethodGet, itemImagePath+"?type=Primary", "", cookie)
	if response.Code != http.StatusNotFound {
		t.Fatalf("response = %d %s, want 404", response.Code, response.Body.String())
	}
}

func TestItemImageRequiresOneReadPermission(t *testing.T) {
	h := newAuthHarness(t, nil)
	accountID := h.store.accounts["alice"].ID
	h.authorization.permissions[accountID] = nil
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	request := h.request(t, http.MethodGet, itemImagePath+"?type=Primary", "", cookie)
	if request.Code != http.StatusForbidden || h.mediaServers.imageCalls != 0 {
		t.Fatalf("response = %d %s, calls = %d", request.Code, request.Body.String(), h.mediaServers.imageCalls)
	}
}

func TestItemImageOpenAPIContract(t *testing.T) {
	document := loadOpenAPI(t)
	operation := document.validator.Paths.Find(
		"/api/v1/media-servers/{id}/items/{item_id}/image",
	).Get
	if operation == nil {
		t.Fatal("item image operation is missing")
	}
	assertItemImageParameters(t, operation.Parameters)
	response := operation.Responses.Value("200")
	if response == nil || response.Value == nil {
		t.Fatal("item image 200 response is missing")
	}
	wantContent := []string{"image/jpeg", "image/png", "image/webp"}
	gotContent := make([]string, 0, len(response.Value.Content))
	for mediaType := range response.Value.Content {
		gotContent = append(gotContent, mediaType)
	}
	slices.Sort(gotContent)
	if !slices.Equal(gotContent, wantContent) {
		t.Fatalf("image content = %v, want %v", gotContent, wantContent)
	}
	for _, status := range []string{"200", "304", "401", "403", "404", "422", "500", "502", "503", "405"} {
		if operation.Responses.Value(status) == nil {
			t.Errorf("response %s is missing", status)
		}
	}
}

func assertItemImageParameters(t *testing.T, parameters openapi3.Parameters) {
	t.Helper()
	found := make(map[string]bool, len(parameters))
	for _, reference := range parameters {
		parameter := reference.Value
		found[parameter.Name] = true
		schema := parameter.Schema.Value
		switch parameter.Name {
		case "type":
			if !parameter.Required || !slices.Equal(schema.Enum, []any{"Primary", "Backdrop", "Thumb"}) {
				t.Errorf("type parameter = %+v", parameter)
			}
		case "max_width":
			if schema.Min == nil || *schema.Min != core.MinItemImageWidth || schema.Max == nil ||
				*schema.Max != core.MaxItemImageWidth || schema.Default != float64(defaultItemImageWidth) {
				t.Errorf("max_width schema = %+v", schema)
			}
		case "item_id":
			if !parameter.Required || schema.Format != "" || schema.Pattern != itemImagePattern {
				t.Errorf("item_id schema = %+v", schema)
			}
		}
	}
	for _, name := range []string{"id", "item_id", "type", "max_width", "If-None-Match"} {
		if !found[name] {
			t.Errorf("parameter %q is missing", name)
		}
	}
}
