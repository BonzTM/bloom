package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const importTestServerID = "22222222-2222-4222-8222-222222222222"

type fakeImportManager struct {
	job       core.ImportJob
	jobs      []core.ImportJob
	err       error
	upload    string
	requested string
	stagingID string
	discarded string
	uploadErr error
}

func (f *fakeImportManager) CreatePlaybackReporting(_ context.Context, _, requested string) (core.ImportJob, error) {
	f.requested = requested
	return f.job, f.err
}

func (f *fakeImportManager) CheckBloomExportUpload() error { return f.uploadErr }

func (f *fakeImportManager) StageBloomExport(_ context.Context, upload io.Reader) (string, error) {
	if f.uploadErr != nil {
		return "", f.uploadErr
	}
	f.stagingID = "44444444-4444-4444-8444-444444444444"
	data, err := io.ReadAll(upload)
	f.upload = string(data)
	return f.stagingID, err
}

func (f *fakeImportManager) CreateBloomExport(_ context.Context, _, requested, stagingID string) (core.ImportJob, error) {
	f.requested = requested
	if stagingID != f.stagingID {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	return f.job, f.err
}

func (f *fakeImportManager) DiscardBloomExport(stagingID string) error {
	f.discarded = stagingID
	return nil
}

func (f *fakeImportManager) List(context.Context, core.ImportListQuery) ([]core.ImportJob, error) {
	return f.jobs, f.err
}

func (f *fakeImportManager) Get(context.Context, string) (core.ImportJob, error) {
	return f.job, f.err
}

func (f *fakeImportManager) Cancel(context.Context, string) (core.ImportJob, error) {
	return f.job, f.err
}

func TestCreatePlaybackReportingImportMatchesOpenAPI(t *testing.T) {
	server, manager, audit := importHandlerServer(t)
	body := `{"media_server_id":"` + importTestServerID + `","source":"playback_reporting"}`
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", body, core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if recorder.Code != http.StatusCreated || manager.requested != testRequestAccountID {
		t.Fatalf("create import = %d requested %q: %s", recorder.Code, manager.requested, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/ImportJob")
	if event := audit.last(t); event.Action != "import.create" || event.Resource != "import:"+manager.job.ID {
		t.Fatalf("audit event = %+v", event)
	}
}

func TestCreateBloomExportAcceptsBoundedNDJSONPart(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	payload := "{\"id\":\"watch\"}\n"
	body, contentType := bloomMultipart(t, payload)
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if recorder.Code != http.StatusCreated || manager.upload != payload {
		t.Fatalf("multipart import = %d upload %q: %s", recorder.Code, manager.upload, recorder.Body.String())
	}
}

func TestCreateBloomExportUnavailableDoesNotReadBody(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	manager.uploadErr = &core.InvalidArgumentError{
		Field: "source", Code: "unavailable",
		Message: "This Bloom cannot store uploads; set BLOOM_DATA_DIR to a writable directory.",
	}
	body := &countingReadCloser{Reader: strings.NewReader("must not be read")}
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
	request.Body = body
	request.Header.Set("Content-Type", "multipart/form-data; boundary=unused")
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if body.reads != 0 {
		t.Fatalf("multipart body reads = %d, want 0", body.reads)
	}
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unavailable upload = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code   string `json:"code"`
		Fields []struct {
			Field, Code, Message string
		} `json:"fields"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode unavailable response: %v", err)
	}
	if response.Code != codeValidationFailed || len(response.Fields) != 1 ||
		response.Fields[0].Field != "source" || response.Fields[0].Code != "unavailable" ||
		response.Fields[0].Message != manager.uploadErr.Error() {
		t.Fatalf("unavailable response = %+v", response)
	}
}

type countingReadCloser struct {
	io.Reader
	reads int
}

func (r *countingReadCloser) Read(buffer []byte) (int, error) {
	r.reads++
	return r.Reader.Read(buffer)
}

func (*countingReadCloser) Close() error { return nil }

func TestCreateBloomExportRejectsDuplicateAndUnknownParts(t *testing.T) {
	for _, partName := range []string{"source", "unexpected"} {
		t.Run(partName, func(t *testing.T) {
			server, manager, _ := importHandlerServer(t)
			body, contentType := bloomMultipartWithExtra(t, "{}\n", partName)
			request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
			request.Body = io.NopCloser(bytes.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			recorder := httptest.NewRecorder()
			server.handleCreateImport(recorder, request)
			if recorder.Code != http.StatusUnprocessableEntity || manager.discarded != manager.stagingID {
				t.Fatalf("part %q = %d discarded %q: %s", partName, recorder.Code, manager.discarded, recorder.Body.String())
			}
		})
	}
}

func TestUnsupportedCreateMediaTypeEmitsFailureAudit(t *testing.T) {
	server, _, audit := importHandlerServer(t)
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
	request.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported media type = %d", recorder.Code)
	}
	event := audit.last(t)
	if event.Action != "import.create" || event.Result != telemetry.AuditFailure {
		t.Fatalf("audit event = %+v", event)
	}
}

func TestListImportsMatchesOpenAPIAndRejectsBadCursor(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	manager.jobs = []core.ImportJob{manager.job}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/imports?page_size=50", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleListImports(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list imports = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/ImportsResponse")
	bad := requestWithAccount(t, http.MethodGet, "/api/v1/imports?cursor=bad", "", core.PermissionAdminSettings)
	badRecorder := httptest.NewRecorder()
	server.handleListImports(badRecorder, bad)
	if badRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad cursor = %d, want 422", badRecorder.Code)
	}
}

func TestGetAndCancelImportMatchOpenAPI(t *testing.T) {
	server, manager, audit := importHandlerServer(t)
	document := loadOpenAPI(t)
	get := requestWithAccount(t, http.MethodGet, "/api/v1/imports/"+manager.job.ID, "", core.PermissionAdminSettings)
	get.SetPathValue("id", manager.job.ID)
	getRecorder := httptest.NewRecorder()
	server.handleGetImport(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get import = %d: %s", getRecorder.Code, getRecorder.Body.String())
	}
	assertJSONMatchesSchema(t, document, getRecorder.Body.Bytes(), "#/components/schemas/ImportJob")
	manager.job.State = core.ImportCancelled
	finished := manager.job.UpdatedAt.Add(time.Minute)
	manager.job.FinishedAt = &finished
	cancel := requestWithAccount(t, http.MethodPost, "/api/v1/imports/"+manager.job.ID+"/cancel", "", core.PermissionAdminSettings)
	cancel.SetPathValue("id", manager.job.ID)
	cancelRecorder := httptest.NewRecorder()
	server.handleCancelImport(cancelRecorder, cancel)
	if cancelRecorder.Code != http.StatusOK {
		t.Fatalf("cancel import = %d: %s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
	assertJSONMatchesSchema(t, document, cancelRecorder.Body.Bytes(), "#/components/schemas/ImportJob")
	if event := audit.last(t); event.Action != "import.cancel" || event.Reason != "cancelled" {
		t.Fatalf("cancel audit = %+v", event)
	}
}

func TestCreateImportConflictUsesDocumentedCode(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	manager.err = core.ErrImportInProgress
	body := `{"media_server_id":"` + importTestServerID + `","source":"playback_reporting"}`
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", body, core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflict = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["code"] != codeImportInProgress {
		t.Fatalf("conflict response = %v, %v", response, err)
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/ErrorResponse")
}

func TestCreateImportUnknownServerMatchesDocumented404(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	manager.err = core.ErrNotFound
	body := `{"media_server_id":"` + importTestServerID + `","source":"playback_reporting"}`
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", body, core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown server = %d: %s", recorder.Code, recorder.Body.String())
	}
	document := loadOpenAPI(t)
	if _, ok := document.Paths["/api/v1/imports"]["post"].Responses["404"]; !ok {
		t.Fatal("create import does not document 404")
	}
	assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), "#/components/schemas/ErrorResponse")
}

func TestWatchExportJSONLMatchesDocumentedRecord(t *testing.T) {
	server, _, audit := importHandlerServer(t)
	ended := time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC)
	server.playbackReader = &fakePlaybackReader{watches: []core.PlaybackWatch{exportHTTPWatch(ended)}}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/exports/watches?limit=1", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleExportWatches(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("export = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	var value any
	if err := json.Unmarshal(bytes.TrimSpace(recorder.Body.Bytes()), &value); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if err := validateOpenAPIValue(loadOpenAPI(t), "#/components/schemas/WatchExportRecord", value); err != nil {
		t.Fatalf("export record contract: %v; body %s", err, recorder.Body.Bytes())
	}
	event := audit.last(t)
	if event.Actor != testRequestAccountID || event.Action != "watch.export" || event.Resource != "watches" ||
		event.Result != telemetry.AuditSuccess || event.RequestID != "request-1" || event.Source == "" {
		t.Fatalf("export audit = %+v", event)
	}
}

func TestWatchExportValidationFailureIsAuditedWithoutQueryData(t *testing.T) {
	server, _, audit := importHandlerServer(t)
	request := requestWithAccount(t, http.MethodGet,
		"/api/v1/exports/watches?limit=10001&cursor=sensitive", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleExportWatches(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid export = %d: %s", recorder.Code, recorder.Body.String())
	}
	event := audit.last(t)
	if event.Actor != testRequestAccountID || event.Action != "watch.export" || event.Resource != "watches" ||
		event.Result != telemetry.AuditFailure || event.Reason != "invalid" || event.RequestID != "request-1" ||
		event.Source == "" || strings.Contains(event.Resource, "sensitive") {
		t.Fatalf("export failure audit = %+v", event)
	}
}

func importHandlerServer(t *testing.T) (*Server, *fakeImportManager, *recordingAudit) {
	t.Helper()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	manager := &fakeImportManager{job: core.ImportJob{
		ID: "11111111-1111-4111-8111-111111111111", MediaServerID: importTestServerID,
		RequestedBy: testRequestAccountID, Source: core.ImportSourcePlaybackReporting,
		State: core.ImportPending, CreatedAt: now, UpdatedAt: now,
	}}
	audit := &recordingAudit{}
	server := &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 8192, imports: manager,
		audit: audit, auditFailureMetrics: telemetry.NopMetrics{},
	}
	return server, manager, audit
}

func bloomMultipart(t *testing.T, payload string) ([]byte, string) {
	t.Helper()
	return bloomMultipartWithExtra(t, payload, "")
}

func bloomMultipartWithExtra(t *testing.T, payload, extraName string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("media_server_id", importTestServerID); err != nil {
		t.Fatalf("write media_server_id: %v", err)
	}
	if err := writer.WriteField("source", "bloom_export"); err != nil {
		t.Fatalf("write source: %v", err)
	}
	header := make(textproto.MIMEHeader)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="watches.jsonl"`}
	header["Content-Type"] = []string{"application/x-ndjson"}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader(payload)); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if extraName != "" {
		if err := writer.WriteField(extraName, "duplicate"); err != nil {
			t.Fatalf("write extra part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func exportHTTPWatch(ended time.Time) core.PlaybackWatch {
	return core.PlaybackWatch{
		ID: "33333333-3333-4333-8333-333333333333", MediaServerID: importTestServerID,
		MediaServerName: "Jellyfin", MediaUserID: "user", Username: "Alice", DeviceID: "device",
		DeviceName: "TV", Client: "Web", ItemID: "item", ItemName: "Film", ItemType: "Movie",
		PlayMethod: core.PlayMethodDirectPlay, Source: core.WatchSourcePoll, State: core.WatchStopped,
		ActiveTime: time.Minute, StartedAt: ended.Add(-time.Minute), EndedAt: &ended,
	}
}
