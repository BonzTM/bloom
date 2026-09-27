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

type metadataReaderStub struct {
	discoverInput core.MetadataDiscover
	accountID     string
}

func (*metadataReaderStub) Search(context.Context, core.MetadataSearch) ([]core.MetadataTitle, error) {
	return nil, nil
}

func (*metadataReaderStub) Movie(context.Context, string) (core.MetadataTitle, error) {
	return core.MetadataTitle{}, nil
}

func (*metadataReaderStub) Series(context.Context, string, bool) (core.MetadataSeries, error) {
	return core.MetadataSeries{}, nil
}

func (s *metadataReaderStub) Discover(
	_ context.Context, accountID string, input core.MetadataDiscover,
) (core.MetadataDiscoverPage, error) {
	s.accountID, s.discoverInput = accountID, input
	return core.MetadataDiscoverPage{Items: []core.MetadataDiscoverItem{{
		MetadataTitle: core.MetadataTitle{
			Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: "11", Title: "Film",
			Year: 2026, PosterPath: "/poster.jpg", BackdropPath: "/backdrop.jpg", Overview: "Plot",
		},
		RequestState: core.MetadataRequestPending,
	}}, Page: input.Page, TotalPages: 3}, nil
}

func (*metadataReaderStub) Genres(context.Context, core.MediaKind) ([]core.MetadataGenre, error) {
	return []core.MetadataGenre{{ID: 28, Name: "Action"}}, nil
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

func TestMetadataDiscoverReturnsRequestStateAndCursor(t *testing.T) {
	reader := &metadataReaderStub{}
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataDiscovery: reader}
	request := requestWithAccount(
		t, http.MethodGet, "/api/v1/metadata/discover/trending?cursor=Mg", "", core.PermissionRequestsReadOwn,
	)
	recorder := httptest.NewRecorder()
	server.handleMetadataTrending(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/MetadataDiscoverResponse")
	var response metadataDiscoverResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if reader.discoverInput != (core.MetadataDiscover{List: core.MetadataTrending, Page: 2}) || reader.accountID == "" {
		t.Fatalf("discover input = %+v account = %q", reader.discoverInput, reader.accountID)
	}
	if len(response.Items) != 1 || response.Items[0].RequestState != core.MetadataRequestPending ||
		response.Items[0].BackdropPath != "/backdrop.jpg" || response.NextCursor != "Mw" {
		t.Fatalf("response = %+v", response)
	}
}

func TestMetadataDiscoverRejectsInvalidCursor(t *testing.T) {
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataDiscovery: &metadataReaderStub{}}
	request := requestWithAccount(
		t, http.MethodGet, "/api/v1/metadata/discover/movies/popular?cursor=MjE", "", core.PermissionRequestsCreate,
	)
	recorder := httptest.NewRecorder()
	server.handleMetadataMoviesPopular(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"field":"cursor"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestMetadataGenresReturnsProviderGenres(t *testing.T) {
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataDiscovery: &metadataReaderStub{}}
	request := requestWithAccount(
		t, http.MethodGet, "/api/v1/metadata/genres?kind=movie", "", core.PermissionRequestsReadOwn,
	)
	recorder := httptest.NewRecorder()
	server.handleMetadataGenres(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"items":[{"id":28,"name":"Action"}]}`+"\n" {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/MetadataGenresResponse")
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
