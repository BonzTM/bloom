package http

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/importer"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const importTestServerID = "22222222-2222-4222-8222-222222222222"

type fakeImportManager struct {
	job              core.ImportJob
	jobs             []core.ImportJob
	err              error
	upload           string
	requested        string
	stagingID        string
	discarded        string
	uploadErr        error
	stageSeen        chan struct{}
	stageHasDeadline bool
}

func (f *fakeImportManager) CreatePlaybackReporting(_ context.Context, _, requested string) (core.ImportJob, error) {
	f.requested = requested
	return f.job, f.err
}

func (f *fakeImportManager) StageBloomExport(ctx context.Context, upload io.Reader) (string, error) {
	if f.uploadErr != nil {
		return "", f.uploadErr
	}
	_, f.stageHasDeadline = ctx.Deadline()
	f.stagingID = "44444444-4444-4444-8444-444444444444"
	if f.stageSeen != nil {
		close(f.stageSeen)
	}
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

func (f *fakeImportManager) DiscardBloomExport(_ context.Context, stagingID string) error {
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
	if recorder.Code != http.StatusCreated || manager.upload != payload || !manager.stageHasDeadline {
		t.Fatalf("multipart import = %d upload %q: %s", recorder.Code, manager.upload, recorder.Body.String())
	}
}

func TestCreateBloomExportAcceptsZipPartWithoutUsingFilename(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	payload := string([]byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'})
	body, contentType := bloomMultipartWithType(t, payload, "application/zip", "export.data")
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	server.handleCreateImport(recorder, request)
	if recorder.Code != http.StatusCreated || manager.upload != payload {
		t.Fatalf("ZIP multipart import = %d upload %q: %s", recorder.Code, manager.upload, recorder.Body.String())
	}
}

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

func TestWatchExportZipMatchesContractAndImportCodec(t *testing.T) {
	server, manager, audit := importHandlerServer(t)
	manager.jobs = []core.ImportJob{manager.job}
	ended := time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC)
	server.playbackReader = &fakePlaybackReader{watches: []core.PlaybackWatch{exportHTTPWatch(ended)}}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/exports/watches", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleExportWatches(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/zip" ||
		!strings.HasPrefix(recorder.Header().Get("Content-Disposition"), `attachment; filename="bloom-export-`) {
		t.Fatalf("export = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	entries := readExportZip(t, recorder.Body.Bytes())
	if strings.Join(entries.names, ",") != "manifest.json,watches.jsonl,imports.jsonl,summary.json" {
		t.Fatalf("zip entries = %v", entries.names)
	}
	if entries.methods["watches.jsonl"] != zip.Store || entries.flags["watches.jsonl"]&0x8 == 0 {
		t.Fatalf("watches.jsonl method=%d flags=%#x, want stored data descriptor",
			entries.methods["watches.jsonl"], entries.flags["watches.jsonl"])
	}
	document := loadOpenAPI(t)
	assertJSONContract(t, document, "#/components/schemas/WatchExportManifest", entries.data["manifest.json"])
	watchLine := bytes.TrimSpace(entries.data["watches.jsonl"])
	assertJSONContract(t, document, "#/components/schemas/WatchExportRecord", watchLine)
	if record, err := importer.DecodeWatchJSONL(watchLine); err != nil || record.RecordID == "" {
		t.Fatalf("import exported watch = %+v, %v", record, err)
	}
	assertJSONContract(t, document, "#/components/schemas/ImportJob", bytes.TrimSpace(entries.data["imports.jsonl"]))
	assertJSONContract(t, document, "#/components/schemas/WatchExportSummary", entries.data["summary.json"])
	var summary watchExportSummary
	if err := json.Unmarshal(entries.data["summary.json"], &summary); err != nil ||
		summary.WatchesBytes != int64(len(entries.data["watches.jsonl"])) ||
		summary.ImportsBytes != int64(len(entries.data["imports.jsonl"])) {
		t.Fatalf("export summary sizes = %+v, %v", summary, err)
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
		"/api/v1/exports/watches?limit=1&cursor=sensitive", "", core.PermissionAdminSettings)
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

func TestWatchExportOpenAPIOnlyDocumentsMediaServerFilter(t *testing.T) {
	operation := loadOpenAPI(t).validator.Paths.Find("/api/v1/exports/watches").Get
	if len(operation.Parameters) != 1 || operation.Parameters[0].Value.Name != "media_server_id" {
		t.Fatalf("export parameters = %+v, want only media_server_id", operation.Parameters)
	}
	response := operation.Responses.Value("200").Value
	if response.Content.Get("application/zip") == nil || response.Headers["X-Next-Cursor"] != nil {
		t.Fatalf("export response does not match ZIP contract: %+v", response)
	}
}

func TestWatchExportFirstQueryFailureReturnsDocumented500(t *testing.T) {
	server, _, audit := importHandlerServer(t)
	server.playbackReader = &fakePlaybackReader{err: errors.New("database unavailable")}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/exports/watches", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleExportWatches(recorder, request)
	if recorder.Code != http.StatusInternalServerError ||
		!strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("first query failure = %d %q: %s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/ErrorResponse")
	if event := audit.last(t); event.Result != telemetry.AuditFailure || event.Reason != "failed" {
		t.Fatalf("failure audit = %+v", event)
	}
}

func TestWatchExportFirstImportQueryFailureReturnsDocumented500(t *testing.T) {
	server, manager, audit := importHandlerServer(t)
	server.playbackReader = &fakePlaybackReader{}
	manager.err = errors.New("database unavailable")
	request := requestWithAccount(t, http.MethodGet, "/api/v1/exports/watches", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	server.handleExportWatches(recorder, request)
	if recorder.Code != http.StatusInternalServerError ||
		!strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("first import query failure = %d %q: %s",
			recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/ErrorResponse")
	if event := audit.last(t); event.Result != telemetry.AuditFailure || event.Reason != "failed" {
		t.Fatalf("failure audit = %+v", event)
	}
}

func TestWatchExportMidstreamFailureAbortsTransfer(t *testing.T) {
	server, _, audit := importHandlerServer(t)
	watches := make([]core.PlaybackWatch, exportBatchSize+1)
	ended := time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC)
	for index := range watches {
		watches[index] = exportHTTPWatch(ended.Add(-time.Duration(index) * time.Minute))
	}
	server.playbackReader = &fakePlaybackReader{
		watches: watches, errorsByCall: []error{nil, errors.New("database unavailable")},
	}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/exports/watches", "", core.PermissionAdminSettings)
	recorder := httptest.NewRecorder()
	handler := recoverMiddleware(server.logger)(http.HandlerFunc(server.handleExportWatches))
	recovered := capturePanic(func() { handler.ServeHTTP(recorder, request) })
	abortErr, ok := recovered.(error)
	if !ok || !errors.Is(abortErr, http.ErrAbortHandler) || recorder.Code != http.StatusOK ||
		recorder.Header().Get("X-Next-Cursor") != "" {
		t.Fatalf("midstream failure = panic %v status %d", recovered, recorder.Code)
	}
	if event := audit.last(t); event.Result != telemetry.AuditFailure || event.Reason != "failed" {
		t.Fatalf("failure audit = %+v", event)
	}
}

func capturePanic(call func()) any {
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		call()
	}()
	return recovered
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	readDeadline, writeDeadline time.Time
	writeDelay                  time.Duration
}

func (r *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	r.readDeadline = deadline
	return nil
}

func (r *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	r.writeDeadline = deadline
	return nil
}

func (r *deadlineRecorder) Write(data []byte) (int, error) {
	time.Sleep(r.writeDelay)
	if !r.writeDeadline.IsZero() && time.Now().After(r.writeDeadline) {
		return 0, context.DeadlineExceeded
	}
	return r.ResponseRecorder.Write(data)
}

type slowDeadlineBody struct {
	reader   io.Reader
	deadline *time.Time
}

func (r slowDeadlineBody) Read(data []byte) (int, error) {
	time.Sleep(10 * time.Millisecond)
	if time.Now().After(*r.deadline) {
		return 0, context.DeadlineExceeded
	}
	return r.reader.Read(data)
}

func (slowDeadlineBody) Close() error { return nil }

func TestCreateBloomExportExtendsDeadlineForSlowReader(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	server.importTransferTimeout = 30 * time.Second
	body, contentType := bloomMultipart(t, "{}\n")
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder(), readDeadline: time.Now().Add(time.Millisecond)}
	request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
	request.Body = slowDeadlineBody{reader: bytes.NewReader(body), deadline: &recorder.readDeadline}
	request.Header.Set("Content-Type", contentType)
	handler := loggingMiddleware(server.logger, telemetry.NopMetrics{})(http.HandlerFunc(server.handleCreateImport))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || manager.upload != "{}\n" ||
		time.Until(recorder.readDeadline) < time.Second || time.Until(recorder.writeDeadline) < time.Second {
		t.Fatalf(
			"slow upload = %d read deadline %s write deadline %s body %q",
			recorder.Code, time.Until(recorder.readDeadline), time.Until(recorder.writeDeadline), manager.upload,
		)
	}
}

func TestCreateBloomExportExtendsWriteDeadlineForSlowBody(t *testing.T) {
	server, manager, _ := importHandlerServer(t)
	server.importTransferTimeout = 2 * time.Second
	manager.stageSeen = make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), accountKey, core.Account{ID: testRequestAccountID})
		ctx = context.WithValue(ctx, requestIDKey, "request-1")
		server.handleCreateImport(w, r.WithContext(ctx))
	})
	client := newPipeHTTPClient(t, handler, 20*time.Millisecond)
	bodyReader, bodyWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(bodyWriter)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://bloom.test/imports", bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	releaseUpload := make(chan struct{})
	writeDone := make(chan error, 1)
	go writeSlowMultipart(releaseUpload, bodyWriter, multipartWriter, writeDone)
	responseDone := make(chan struct {
		response *http.Response
		err      error
	}, 1)
	go func() {
		response, requestErr := client.Do(request)
		responseDone <- struct {
			response *http.Response
			err      error
		}{response: response, err: requestErr}
	}()
	<-manager.stageSeen
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	close(releaseUpload)
	if err := <-writeDone; err != nil {
		t.Fatalf("write multipart: %v", err)
	}
	result := <-responseDone
	if result.err != nil {
		t.Fatalf("slow upload request: %v", result.err)
	}
	defer result.response.Body.Close()
	if result.response.StatusCode != http.StatusCreated {
		t.Fatalf("slow upload status = %d, want %d", result.response.StatusCode, http.StatusCreated)
	}
}

func newPipeHTTPClient(t *testing.T, handler http.Handler, writeTimeout time.Duration) *http.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newSingleConnListener(serverConn)
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: time.Second, WriteTimeout: writeTimeout,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	var dialOnce sync.Once
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		var connection net.Conn
		dialOnce.Do(func() { connection = clientConn })
		if connection == nil {
			return nil, errors.New("test connection already used")
		}
		return connection, nil
	}}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		_ = server.Close()
		<-serveDone
	})
	return &http.Client{Transport: transport}
}

type singleConnListener struct {
	conn      net.Conn
	closed    chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.conn != nil {
		connection := l.conn
		l.conn = nil
		l.mu.Unlock()
		return connection, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*singleConnListener) Addr() net.Addr { return testAddr("pipe") }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }

func writeSlowMultipart(
	release <-chan struct{}, body *io.PipeWriter, writer *multipart.Writer, done chan<- error,
) {
	err := writer.WriteField("media_server_id", importTestServerID)
	if err == nil {
		err = writer.WriteField("source", "bloom_export")
	}
	var part io.Writer
	if err == nil {
		header := make(textproto.MIMEHeader)
		header["Content-Disposition"] = []string{`form-data; name="file"; filename="watches.jsonl"`}
		header["Content-Type"] = []string{"application/x-ndjson"}
		part, err = writer.CreatePart(header)
	}
	if err == nil {
		<-release
		_, err = io.WriteString(part, "{}\n")
	}
	if err == nil {
		err = writer.Close()
	}
	if closeErr := body.CloseWithError(err); err == nil {
		err = closeErr
	}
	done <- err
}

func TestWatchExportExtendsDeadlineForSlowWriter(t *testing.T) {
	server, _, _ := importHandlerServer(t)
	server.importTransferTimeout = 30 * time.Second
	ended := time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC)
	server.playbackReader = &fakePlaybackReader{watches: []core.PlaybackWatch{exportHTTPWatch(ended)}}
	recorder := &deadlineRecorder{
		ResponseRecorder: httptest.NewRecorder(), writeDeadline: time.Now().Add(time.Millisecond),
		writeDelay: 10 * time.Millisecond,
	}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/exports/watches", "", core.PermissionAdminSettings)
	handler := loggingMiddleware(server.logger, telemetry.NopMetrics{})(http.HandlerFunc(server.handleExportWatches))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 || time.Until(recorder.writeDeadline) < time.Second {
		t.Fatalf("slow export = %d deadline %s bytes %d", recorder.Code, time.Until(recorder.writeDeadline), recorder.Body.Len())
	}
}

type exportZipEntries struct {
	names   []string
	data    map[string][]byte
	methods map[string]uint16
	flags   map[string]uint16
}

func readExportZip(t *testing.T, payload []byte) exportZipEntries {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("open export zip: %v", err)
	}
	entries := exportZipEntries{
		data: make(map[string][]byte), methods: make(map[string]uint16), flags: make(map[string]uint16),
	}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %s: %v", file.Name, errors.Join(readErr, closeErr))
		}
		entries.names = append(entries.names, file.Name)
		entries.data[file.Name] = data
		entries.methods[file.Name] = file.Method
		entries.flags[file.Name] = file.Flags
	}
	return entries
}

func assertJSONContract(t *testing.T, document openAPIDocument, schema string, data []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", schema, err)
	}
	if err := validateOpenAPIValue(document, schema, value); err != nil {
		t.Fatalf("%s contract: %v; body %s", schema, err, data)
	}
}

func TestImportTransferOverrideLeavesGlobalDeadlinesConfigured(t *testing.T) {
	cfg := config.HTTPConfig{
		Addr: ":0", ReadHeaderTimeout: time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute,
	}
	server := newHTTPServer(cfg, http.NotFoundHandler())
	if server.ReadTimeout != 15*time.Second || server.WriteTimeout != 15*time.Second {
		t.Fatalf("global deadlines = read %s write %s", server.ReadTimeout, server.WriteTimeout)
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
		audit: audit, auditFailureMetrics: telemetry.NopMetrics{}, importTransferTimeout: 10 * time.Minute,
	}
	return server, manager, audit
}

func bloomMultipart(t *testing.T, payload string) ([]byte, string) {
	t.Helper()
	return bloomMultipartWithExtra(t, payload, "")
}

func bloomMultipartWithExtra(t *testing.T, payload, extraName string) ([]byte, string) {
	t.Helper()
	body, contentType := bloomMultipartWithType(t, payload, "application/x-ndjson", "watches.jsonl")
	if extraName == "" {
		return body, contentType
	}
	return bloomMultipartWithExtraAndType(t, payload, extraName, "application/x-ndjson", "watches.jsonl")
}

func bloomMultipartWithType(t *testing.T, payload, mediaType, filename string) ([]byte, string) {
	t.Helper()
	return bloomMultipartWithExtraAndType(t, payload, "", mediaType, filename)
}

func bloomMultipartWithExtraAndType(
	t *testing.T, payload, extraName, mediaType, filename string,
) ([]byte, string) {
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
	header["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename)}
	header["Content-Type"] = []string{mediaType}
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
