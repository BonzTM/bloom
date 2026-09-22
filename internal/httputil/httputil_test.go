package httputil

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type payload struct {
	Name string `json:"name"`
}

func TestWriteJSONSetsContentTypeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteJSON(rec, http.StatusCreated, payload{Name: "x"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", got, ContentTypeJSON)
	}
	var got payload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Name != "x" {
		t.Errorf("body = %q (%v), want name x", rec.Body.String(), err)
	}
}

func TestWriteErrorEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteError(rec, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "gone", RequestID: "r1"})
	if err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["code"] != "not_found" || got["message"] != "gone" || got["request_id"] != "r1" {
		t.Errorf("envelope = %v", got)
	}
	if _, present := got["fields"]; present {
		t.Errorf("empty fields must be omitted, got %v", got["fields"])
	}
}

func TestDecodeJSONStrict(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid", `{"name":"a"}`, false},
		{"unknown field", `{"name":"a","extra":1}`, true},
		{"trailing object", `{"name":"a"}{"name":"b"}`, true},
		{"empty", ``, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			_, err := DecodeJSON[payload](httptest.NewRecorder(), req, 1<<10)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodeJSON(%q) err = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
		})
	}
}

func TestDecodeJSONBounded(t *testing.T) {
	body := `{"name":"` + strings.Repeat("x", 64) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	_, err := DecodeJSON[payload](httptest.NewRecorder(), req, 16)
	mbe, ok := errors.AsType[*http.MaxBytesError](err)
	if !ok || mbe.Limit != 16 {
		t.Fatalf("DecodeJSON oversized err = %v, want *http.MaxBytesError with limit 16", err)
	}
}

func TestDecodeJSONRejectsExcessiveNesting(t *testing.T) {
	valid := `{"name":"a","extra":` + strings.Repeat("[", MaxJSONNestingDepth-1) + `0` + strings.Repeat("]", MaxJSONNestingDepth-1) + `}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(valid))
	if _, err := DecodeJSON[payload](httptest.NewRecorder(), req, 1<<10); errors.Is(err, ErrJSONNestingTooDeep) {
		t.Fatalf("depth at limit rejected as too deep: %v", err)
	}

	deep := `{"name":"a","extra":` + strings.Repeat("[", MaxJSONNestingDepth) + `0` + strings.Repeat("]", MaxJSONNestingDepth) + `}`
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(deep))
	if _, err := DecodeJSON[payload](httptest.NewRecorder(), req, 1<<10); !errors.Is(err, ErrJSONNestingTooDeep) {
		t.Fatalf("excessive nesting error = %v, want ErrJSONNestingTooDeep", err)
	}
}

func TestMaxBytesMiddleware(t *testing.T) {
	var readErr error
	h := MaxBytes(8)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("y", 32)))
	h.ServeHTTP(httptest.NewRecorder(), req)
	mbe, ok := errors.AsType[*http.MaxBytesError](readErr)
	if !ok || mbe.Limit != 8 {
		t.Fatalf("read err = %v, want *http.MaxBytesError with limit 8", readErr)
	}
}
