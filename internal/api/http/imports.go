package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/importer"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	defaultImportPageSize = 50
	maxImportPageSize     = 100
	maxImportCursorBytes  = 256
	defaultExportLimit    = 1000
	maxExportLimit        = 10000
	exportBatchSize       = 500
	importMultipartBytes  = core.MaxImportUploadBytes + (1 << 20)
)

type createImportRequest struct {
	MediaServerID string            `json:"media_server_id"`
	Source        core.ImportSource `json:"source"`
}

type importWire struct {
	ID            string            `json:"id"`
	MediaServerID string            `json:"media_server_id"`
	Source        core.ImportSource `json:"source"`
	State         core.ImportState  `json:"state"`
	Read          int64             `json:"read"`
	Imported      int64             `json:"imported"`
	Skipped       int64             `json:"skipped"`
	Duplicate     int64             `json:"duplicate"`
	LastError     string            `json:"last_error"`
	RequestedBy   string            `json:"requested_by"`
	CreatedAt     time.Time         `json:"created_at"`
	StartedAt     *time.Time        `json:"started_at,omitempty"`
	FinishedAt    *time.Time        `json:"finished_at,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type importsResponse struct {
	Items      []importWire `json:"items"`
	NextCursor string       `json:"next_cursor"`
}

type importCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

type importValidationError struct{ fields []httputil.FieldError }

func (e *importValidationError) Error() string { return "invalid import request" }

func (s *Server) handleCreateImport(w http.ResponseWriter, r *http.Request) {
	account, _ := accountFrom(r.Context())
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var job core.ImportJob
	if err == nil {
		switch mediaType {
		case "application/json":
			job, err = s.createReportingImport(w, r, account.ID)
		case "multipart/form-data":
			if err = s.imports.CheckBloomExportUpload(); err == nil {
				job, err = s.createBloomImport(w, r, account.ID)
			}
		default:
			err = errUnsupportedMediaType
		}
	} else {
		err = errUnsupportedMediaType
	}
	if err != nil {
		s.emitImportAudit(r, "import.create", "import:unresolved", telemetry.AuditFailure, "failed")
		if validation, ok := errors.AsType[*importValidationError](err); ok {
			s.writeValidation(w, r, validation.fields)
			return
		}
		if invalid, ok := errors.AsType[*core.InvalidArgumentError](err); ok {
			s.writeValidation(w, r, []httputil.FieldError{{
				Field: invalid.Field, Code: invalid.Code, Message: invalid.Message,
			}})
			return
		}
		writeError(w, r, s.logger, err)
		return
	}
	s.emitImportAudit(r, "import.create", "import:"+job.ID, telemetry.AuditSuccess, "created")
	writeJSON(w, r, s.logger, http.StatusCreated, importDTO(job))
}

func (s *Server) createReportingImport(
	w http.ResponseWriter, r *http.Request, accountID string,
) (core.ImportJob, error) {
	request, err := httputil.DecodeJSON[createImportRequest](w, r, s.maxBodyBytes)
	if err != nil {
		return core.ImportJob{}, invalidImportField("body", "must be one valid JSON object")
	}
	if request.Source != core.ImportSourcePlaybackReporting {
		return core.ImportJob{}, invalidImportField("source", "must be playback_reporting")
	}
	if !core.ValidID(request.MediaServerID) {
		return core.ImportJob{}, invalidImportField("media_server_id", "must be a valid UUID")
	}
	return s.imports.CreatePlaybackReporting(r.Context(), request.MediaServerID, accountID)
}

func (s *Server) createBloomImport(
	w http.ResponseWriter, r *http.Request, accountID string,
) (job core.ImportJob, result error) {
	if err := s.setImportDeadlines(w); err != nil {
		return core.ImportJob{}, fmt.Errorf("extend import upload deadlines: %w", err)
	}
	r.Body = http.MaxBytesReader(w, r.Body, importMultipartBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return core.ImportJob{}, invalidImportField("body", "must be valid multipart form data")
	}
	fields, err := s.readBloomMultipart(r, reader)
	if fields.stagingID != "" {
		defer func() {
			if result != nil {
				s.discardBloomUpload(r, fields.stagingID)
			}
		}()
	}
	if err != nil {
		return job, err
	}
	if fields.source != string(core.ImportSourceBloomExport) {
		return job, invalidImportField("source", "must be bloom_export")
	}
	if !core.ValidID(fields.serverID) {
		return job, invalidImportField("media_server_id", "must be a valid UUID")
	}
	job, result = s.imports.CreateBloomExport(r.Context(), fields.serverID, accountID, fields.stagingID)
	return job, result
}

type bloomMultipartFields struct {
	serverID, source, stagingID string
	serverSeen, sourceSeen      bool
}

func (s *Server) readBloomMultipart(
	r *http.Request, reader *multipart.Reader,
) (bloomMultipartFields, error) {
	var fields bloomMultipartFields
	for range 4 {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return validateBloomMultipart(fields)
		}
		if err != nil {
			return fields, invalidImportField("body", "must be valid multipart form data")
		}
		if err := s.readBloomPart(r, part, &fields); err != nil {
			return fields, err
		}
	}
	return fields, invalidImportField("body", "contains too many parts")
}

func (s *Server) readBloomPart(
	r *http.Request, part *multipart.Part, fields *bloomMultipartFields,
) error {
	switch part.FormName() {
	case "media_server_id":
		return readUniqueTextPart(part, &fields.serverID, &fields.serverSeen)
	case "source":
		return readUniqueTextPart(part, &fields.source, &fields.sourceSeen)
	case "file":
		if fields.stagingID != "" {
			return invalidImportField("file", "must appear exactly once")
		}
		if part.FileName() == "" || part.Header.Get("Content-Type") != "application/x-ndjson" {
			return errUnsupportedMediaType
		}
		id, err := s.imports.StageBloomExport(r.Context(), part)
		if invalid, unavailable := errors.AsType[*core.InvalidArgumentError](err); unavailable && invalid != nil {
			return err
		}
		if errors.Is(err, core.ErrInvalidArgument) {
			return invalidImportField("file", "must not exceed 256 MiB")
		}
		fields.stagingID = id
		return err
	default:
		return invalidImportField("body", "contains an unknown part")
	}
}

func readUniqueTextPart(part *multipart.Part, destination *string, seen *bool) error {
	if *seen || part.FileName() != "" {
		return invalidImportField(part.FormName(), "must appear exactly once")
	}
	*seen = true
	value, err := io.ReadAll(io.LimitReader(part, 257))
	if err != nil || len(value) > 256 {
		return invalidImportField(part.FormName(), "is too large")
	}
	*destination = string(value)
	return nil
}

func validateBloomMultipart(fields bloomMultipartFields) (bloomMultipartFields, error) {
	if fields.serverID == "" {
		return fields, invalidImportField("media_server_id", "is required")
	}
	if fields.source == "" {
		return fields, invalidImportField("source", "is required")
	}
	if fields.stagingID == "" {
		return fields, invalidImportField("file", "is required")
	}
	return fields, nil
}

func (s *Server) discardBloomUpload(r *http.Request, stagingID string) {
	if err := s.imports.DiscardBloomExport(stagingID); err != nil {
		s.logger.WarnContext(r.Context(), "remove unclaimed import staging file", "error", err)
	}
}

func invalidImportField(field, message string) error {
	return &importValidationError{fields: []httputil.FieldError{{
		Field: field, Code: "invalid", Message: message,
	}}}
}

func (s *Server) handleListImports(w http.ResponseWriter, r *http.Request) {
	query, fields := importListQuery(r)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	size := query.PageSize
	query.PageSize++
	jobs, err := s.imports.List(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	jobs, cursor := importPage(jobs, size)
	items := make([]importWire, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, importDTO(job))
	}
	writeJSON(w, r, s.logger, http.StatusOK, importsResponse{Items: items, NextCursor: cursor})
}

func (s *Server) handleGetImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		s.writeValidation(w, r, invalidImportID())
		return
	}
	job, err := s.imports.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, importDTO(job))
}

func (s *Server) handleCancelImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		s.writeValidation(w, r, invalidImportID())
		return
	}
	job, err := s.imports.Cancel(r.Context(), id)
	if err != nil {
		s.emitImportAudit(r, "import.cancel", "import:"+id, telemetry.AuditFailure, "failed")
		writeError(w, r, s.logger, err)
		return
	}
	s.emitImportAudit(r, "import.cancel", "import:"+id, telemetry.AuditSuccess, "cancelled")
	writeJSON(w, r, s.logger, http.StatusOK, importDTO(job))
}

func invalidImportID() []httputil.FieldError {
	return []httputil.FieldError{{Field: "id", Code: "invalid", Message: "must be a valid UUID"}}
}

func importDTO(job core.ImportJob) importWire {
	return importWire{
		ID: job.ID, MediaServerID: job.MediaServerID, Source: job.Source, State: job.State,
		Read: job.Read, Imported: job.Imported, Skipped: job.Skipped, Duplicate: job.Duplicate,
		LastError: job.LastError, RequestedBy: job.RequestedBy, CreatedAt: job.CreatedAt,
		StartedAt: job.StartedAt, FinishedAt: job.FinishedAt, UpdatedAt: job.UpdatedAt,
	}
}

func importListQuery(r *http.Request) (core.ImportListQuery, []httputil.FieldError) {
	query := core.ImportListQuery{PageSize: defaultImportPageSize}
	values := r.URL.Query()
	fields := make([]httputil.FieldError, 0, 2)
	if raw := values.Get("page_size"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 1 || size > maxImportPageSize {
			fields = append(fields, httputil.FieldError{Field: "page_size", Code: "invalid", Message: "must be 1 through 100"})
		} else {
			query.PageSize = size
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeImportCursor(raw)
		if err != nil {
			fields = append(fields, httputil.FieldError{Field: "cursor", Code: "invalid", Message: "must be a valid cursor"})
		} else {
			query.Before = cursor
		}
	}
	return query, fields
}

func decodeImportCursor(value string) (*core.ImportCursor, error) {
	if len(value) > maxImportCursorBytes {
		return nil, core.ErrInvalidArgument
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	var cursor importCursor
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.CreatedAt.IsZero() || !core.ValidID(cursor.ID) {
		return nil, core.ErrInvalidArgument
	}
	return &core.ImportCursor{CreatedAt: core.NormalizeTime(cursor.CreatedAt), ID: cursor.ID}, nil
}

func importPage(jobs []core.ImportJob, size int) ([]core.ImportJob, string) {
	if len(jobs) <= size {
		return jobs, ""
	}
	page := jobs[:size]
	last := page[len(page)-1]
	data, err := json.Marshal(importCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	if err != nil {
		return page, ""
	}
	return page, base64.RawURLEncoding.EncodeToString(data)
}

func (s *Server) emitImportAudit(
	r *http.Request, action, resource string, result telemetry.AuditResult, reason string,
) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: resource, Result: result, Reason: reason,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}

func (s *Server) handleExportWatches(w http.ResponseWriter, r *http.Request) {
	query, limit, fields := exportQuery(r)
	if len(fields) > 0 {
		s.emitImportAudit(r, "watch.export", "watches", telemetry.AuditFailure, "invalid")
		s.writeValidation(w, r, fields)
		return
	}
	if err := s.setExportWriteDeadline(w); err != nil {
		s.failWatchExport(w, r, fmt.Errorf("extend watch export deadline: %w", err))
		return
	}
	page, cursor, err := s.watchExportPage(r, query, min(exportBatchSize, limit))
	if err != nil {
		s.failWatchExport(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Trailer", "X-Next-Cursor")
	w.WriteHeader(http.StatusOK)
	cursor, err = s.streamWatchExport(w, r, query, limit, page, cursor)
	if err != nil {
		s.abortWatchExport(r, err)
	}
	w.Header().Set("X-Next-Cursor", cursor)
	s.emitImportAudit(r, "watch.export", "watches", telemetry.AuditSuccess, "exported")
}

func (s *Server) failWatchExport(w http.ResponseWriter, r *http.Request, err error) {
	s.emitImportAudit(r, "watch.export", "watches", telemetry.AuditFailure, "failed")
	writeError(w, r, s.logger, err)
}

func (s *Server) abortWatchExport(r *http.Request, err error) {
	s.emitImportAudit(r, "watch.export", "watches", telemetry.AuditFailure, "failed")
	s.logger.ErrorContext(r.Context(), "stream watch export", "error", err)
	panic(http.ErrAbortHandler)
}

func exportQuery(r *http.Request) (core.PlaybackQuery, int, []httputil.FieldError) {
	query, fields := playbackListQuery(r.URL.RawQuery, core.PlaybackQueryHistory, true)
	limit := defaultExportLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxExportLimit {
			fields = append(fields, httputil.FieldError{Field: "limit", Code: "invalid", Message: "must be 1 through 10000"})
		} else {
			limit = parsed
		}
	}
	return query, limit, fields
}

func (s *Server) streamWatchExport(
	w http.ResponseWriter, r *http.Request, query core.PlaybackQuery, limit int,
	page []core.PlaybackWatch, cursor string,
) (string, error) {
	written := 0
	for written < limit {
		for _, watch := range page {
			if err := importer.EncodeWatchJSONL(w, watch); err != nil {
				return "", err
			}
		}
		written += len(page)
		if cursor == "" || written == limit {
			return cursor, nil
		}
		var err error
		query, err = nextExportQuery(query, cursor)
		if err != nil {
			return "", err
		}
		page, cursor, err = s.watchExportPage(r, query, min(exportBatchSize, limit-written))
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (s *Server) watchExportPage(
	r *http.Request, query core.PlaybackQuery, size int,
) ([]core.PlaybackWatch, string, error) {
	query.PageSize = size + 1
	watches, err := s.playbackReader.ListWatches(r.Context(), query)
	if err != nil {
		return nil, "", err
	}
	page, cursor := playbackPage(watches, size)
	return page, cursor, nil
}

func nextExportQuery(query core.PlaybackQuery, cursor string) (core.PlaybackQuery, error) {
	started, id, fields := playbackCursorValues([]string{cursor}, nil)
	if len(fields) > 0 {
		return core.PlaybackQuery{}, errors.New("encode internal export cursor")
	}
	query.BeforeStartedAt, query.BeforeID = started, id
	return query, nil
}

func (s *Server) setImportDeadlines(w http.ResponseWriter) error {
	return errors.Join(
		setTransferDeadline(w, s.importTransferTimeout, true),
		setTransferDeadline(w, s.importTransferTimeout, false),
	)
}

func (s *Server) setExportWriteDeadline(w http.ResponseWriter) error {
	return setTransferDeadline(w, s.importTransferTimeout, false)
}

func setTransferDeadline(w http.ResponseWriter, timeout time.Duration, read bool) error {
	if timeout <= 0 {
		return nil
	}
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(timeout)
	var err error
	if read {
		err = controller.SetReadDeadline(deadline)
	} else {
		err = controller.SetWriteDeadline(deadline)
	}
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
