package http

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const (
	defaultItemImageWidth = 400
	itemImageCacheControl = "private, max-age=86400"
)

type itemImageInput struct {
	imageType core.ItemImageType
	maxWidth  int
}

func (s *Server) handleItemImage(w http.ResponseWriter, r *http.Request) {
	serverID, itemID := r.PathValue("id"), r.PathValue("item_id")
	input, fields := parseItemImageInput(r.URL.RawQuery)
	fields = validateItemImagePath(serverID, itemID, fields)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	image, err := s.mediaServerReader.ItemImage(
		r.Context(), serverID, itemID, input.imageType, input.maxWidth, r.Header.Get("If-None-Match"),
	)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	permitPrivateImageCache(r.Context())
	w.Header().Set("Cache-Control", itemImageCacheControl)
	if image.ETag != "" {
		w.Header().Set("ETag", image.ETag)
	}
	if image.NotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", image.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(image.Body)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(image.Body); err != nil {
		s.logger.WarnContext(r.Context(), "write item image failed", "error", err.Error())
	}
}

func parseItemImageInput(rawQuery string) (itemImageInput, []httputil.FieldError) {
	input := itemImageInput{maxWidth: defaultItemImageWidth}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return input, []httputil.FieldError{{Field: "query", Code: "invalid", Message: "must use valid percent encoding"}}
	}
	fields := make([]httputil.FieldError, 0, 2)
	if len(values["type"]) != 1 || !core.ItemImageType(values["type"][0]).Valid() {
		fields = append(fields, httputil.FieldError{Field: "type", Code: "invalid", Message: "must be one of Primary, Backdrop, or Thumb"})
	} else {
		input.imageType = core.ItemImageType(values["type"][0])
	}
	if widthValues := values["max_width"]; len(widthValues) > 0 {
		width, widthErr := strconv.Atoi(widthValues[0])
		if len(widthValues) != 1 || widthErr != nil || width < core.MinItemImageWidth || width > core.MaxItemImageWidth {
			fields = append(fields, httputil.FieldError{Field: "max_width", Code: "out_of_range", Message: "must be one integer from 64 through 1280"})
		} else {
			input.maxWidth = width
		}
	}
	return input, fields
}

func validateItemImagePath(serverID, itemID string, fields []httputil.FieldError) []httputil.FieldError {
	if !core.ValidID(serverID) {
		fields = append(fields, httputil.FieldError{Field: "id", Code: "invalid", Message: "must be a valid UUID"})
	}
	if !core.ValidAccountMediaUserID(itemID) {
		fields = append(fields, httputil.FieldError{Field: "item_id", Code: "invalid", Message: "must be a valid media item id"})
	}
	return fields
}
