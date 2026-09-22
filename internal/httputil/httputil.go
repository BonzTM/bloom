// Package httputil holds the JSON, error-envelope, and request-size helpers
// shared by Bloom's transport adapters (the JSON API today, the embedded SPA
// handler in internal/api/web when ADR 0002 lands). It is deliberately small,
// per the handbook's foundations/shared-constructs.md: it owns the wire shape
// of a JSON response and nothing else. Domain-error-to-status mapping stays in
// each adapter's errors.go.
package httputil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrorResponse is the single structured error envelope returned by EVERY JSON
// endpoint, per the handbook's foundations/serialization.md ### Error
// Responses. It is a dedicated DTO with explicit snake_case json tags. A bare
// {"error":"..."} string is forbidden: the client gets one human sentence and
// nothing to branch on. The envelope carries a machine-readable code, a safe
// human message, optional per-field validation failures, and the correlation
// request_id in the body.
type ErrorResponse struct {
	// Code is a machine-readable string enum the client may branch on. It is NOT
	// the HTTP status: two 404s with different codes are different failures.
	Code string `json:"code"`
	// Message is human-readable and safe to surface. For 5xx it is generic and
	// never carries internal detail.
	Message string `json:"message"`
	// Fields carries one entry per offending input on a validation failure; it is
	// omitted when empty.
	Fields []FieldError `json:"fields,omitzero"`
	// RequestID is the correlation id, echoed in the body (not only the header) so
	// a client can quote it. Omitted when absent.
	RequestID string `json:"request_id,omitzero"`
}

// FieldError carries one validation failure against one input field.
type FieldError struct {
	// Field is a dotted path into the request body, e.g. "name" or "items.0.qty".
	Field string `json:"field"`
	// Code is a small machine-readable enum: "required", "out_of_range", etc.
	Code string `json:"code"`
	// Message is the human-readable detail for this field.
	Message string `json:"message"`
}

// ContentTypeJSON is the Content-Type every JSON response carries.
const ContentTypeJSON = "application/json; charset=utf-8"

// WriteJSON encodes v as JSON with the given status. The status and headers are
// committed before encoding, so a late encode error cannot change the
// response; it is returned so the caller can log it once (a broken connection
// or marshal bug must be observable, not silent).
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("encode json response: %w", err)
	}
	return nil
}

// WriteError encodes the error envelope with the given status. See WriteJSON
// for the error semantics.
func WriteError(w http.ResponseWriter, status int, resp ErrorResponse) error {
	return WriteJSON(w, status, resp)
}

// ErrBodyNotSingleObject is returned by DecodeJSON when the body holds more than
// one JSON value.
var ErrBodyNotSingleObject = errors.New("request body must contain a single JSON object")

// DecodeJSON bounds the body to maxBytes, strictly decodes a single JSON value
// of type T, and rejects unknown fields. This is an internal, versioned
// surface, so the strict policy (DisallowUnknownFields) applies per the
// serialization doc. An oversized body surfaces as a *http.MaxBytesError.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request, maxBytes int64) (T, error) {
	var v T
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("decode %T: %w", v, err)
	}
	// Reject trailing data so two concatenated objects are not silently
	// accepted as one.
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return v, ErrBodyNotSingleObject
	}
	return v, nil
}

// MaxBytes returns middleware that caps every request body at maxBytes so a
// handler that reads the body without DecodeJSON is still bounded. Streaming
// endpoints must be mounted outside this middleware.
func MaxBytes(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}
