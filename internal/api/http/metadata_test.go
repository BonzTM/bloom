package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const testMetadataReadAccessToken = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhdWQiOiJ0bWRiIiwic3ViIjoiYmxvb20tdGVzdCIsImlhdCI6MTcwMDAwMDAwMH0.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmUtc2lnbmF0dXJl"

type metadataManagerStub struct {
	credential string
	err        error
}

func (m *metadataManagerStub) SetKey(_ context.Context, _ core.MetadataProviderKind, credential string) error {
	m.credential = credential
	return m.err
}

func (*metadataManagerStub) HasKey(context.Context, core.MetadataProviderKind) (bool, error) {
	return false, nil
}

func (*metadataManagerStub) RemoveKey(context.Context, core.MetadataProviderKind) error { return nil }

func TestSetMetadataKeyRequiresReadAccessToken(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "v3 API key", body: `{"api_key":"0123456789abcdef0123456789abcdef"}`},
		{name: "empty", body: `{"api_key":""}`},
		{name: "malformed JWT", body: `{"api_key":"eyJ.not-base64!.signature"}`},
		{name: "whitespace", body: `{"api_key":"` + testMetadataReadAccessToken + ` "}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			manager := &metadataManagerStub{}
			server := metadataHandlerServer(manager)
			request := requestWithAccount(t, http.MethodPut, "/api/v1/metadata/providers/tmdb/key", testCase.body, core.PermissionAdminSettings)
			recorder := httptest.NewRecorder()
			server.handleSetMetadataKey(recorder, request)
			assertReadAccessTokenValidation(t, recorder)
			if manager.credential != "" {
				t.Fatal("invalid credential reached metadata manager")
			}
		})
	}
}

func TestSetMetadataKeyPreservesUnauthorizedProbeClassification(t *testing.T) {
	manager := &metadataManagerStub{err: core.ErrMetadataUnauthorized}
	server := metadataHandlerServer(manager)
	body := `{"api_key":"` + testMetadataReadAccessToken + `"}`
	request := requestWithAccount(t, http.MethodPut, "/api/v1/metadata/providers/tmdb/key", body, core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleSetMetadataKey(recorder, request)
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), codeMetadataProviderFailure) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func metadataHandlerServer(manager metadataManager) *Server {
	return &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 8192, metadataManager: manager,
		audit: &recordingAudit{}, auditFailureMetrics: telemetry.NopMetrics{},
	}
}

func assertReadAccessTokenValidation(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	var response httputil.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := httputil.FieldError{
		Field: "api_key", Code: "read_access_token_required",
		Message: "TMDB needs the API Read Access Token, not the v3 API key",
	}
	if recorder.Code != http.StatusUnprocessableEntity || response.Code != codeValidationFailed ||
		len(response.Fields) != 1 || response.Fields[0] != want {
		t.Fatalf("response = %d %+v, want field %+v", recorder.Code, response, want)
	}
}
