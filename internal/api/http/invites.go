package http

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	inviteapp "github.com/BonzTM/bloom/internal/invite"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	defaultInvitePageSize = 50
	maxInvitePageSize     = 100
	maxInviteCursorBytes  = 128
	usernameRule          = "1-64 UTF-8 bytes; Unicode letters, marks, decimal digits, connector punctuation, spaces, and -'._@+; no surrounding whitespace or controls; not . or .."
	passwordRule          = "15-1024 Unicode characters, at most 4096 UTF-8 bytes, and not in Bloom's common-password denylist" //nolint:gosec // Public validation guidance, not a credential.
)

type createInviteRequest struct {
	MediaServerID string     `json:"media_server_id"`
	Label         string     `json:"label"`
	ExpiresAt     *time.Time `json:"expires_at"`
	MaxUses       *int       `json:"max_uses"`
	LibraryIDs    []string   `json:"library_ids"`
}

type inviteResponse struct {
	ID            string            `json:"id"`
	MediaServerID string            `json:"media_server_id"`
	Label         string            `json:"label"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	MaxUses       *int              `json:"max_uses,omitempty"`
	UseCount      int               `json:"use_count"`
	LibraryIDs    []string          `json:"library_ids"`
	Status        core.InviteStatus `json:"status"`
	CreatedAt     time.Time         `json:"created_at"`
}

type createInviteResponse struct {
	Invite     inviteResponse `json:"invite"`
	Code       string         `json:"code"`
	AcceptPath string         `json:"accept_path"`
}

type invitesResponse struct {
	Items      []inviteResponse `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

type publicInviteResponse struct {
	MediaServerName string `json:"media_server_name"`
	UsernameRule    string `json:"username_rule"`
	PasswordRule    string `json:"password_rule"`
}

type acceptInviteRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type acceptInviteResponse struct {
	MediaServerName string `json:"media_server_name"`
	Username        string `json:"username"`
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeCreateInvite(w, r)
	if !ok {
		s.recordInviteCreate(r, "invite:unresolved", telemetry.AuditFailure, "invalid_input")
		return
	}
	account, _ := accountFrom(r.Context())
	created, err := s.inviteManager.Create(r.Context(), inviteapp.CreateInput{
		MediaServerID: request.MediaServerID, CreatedBy: account.ID, Label: request.Label,
		ExpiresAt: request.ExpiresAt, MaxUses: request.MaxUses, LibraryIDs: request.LibraryIDs,
	})
	if err != nil {
		s.recordInviteCreate(r, "invite:unresolved", telemetry.AuditFailure, inviteFailureReason(err))
		if selectionErr, ok := errors.AsType[*inviteapp.LibrarySelectionError](err); ok && selectionErr != nil {
			s.writeValidation(w, r, []httputil.FieldError{{
				Field: "library_ids", Code: "invalid", Message: "must contain only libraries from the selected media server",
			}})
			return
		}
		if errors.Is(err, core.ErrInvalidArgument) {
			s.writeValidation(w, r, []httputil.FieldError{{
				Field: "body", Code: "invalid", Message: "contains invalid invite values",
			}})
			return
		}
		writeError(w, r, s.logger, err)
		return
	}
	s.recordInviteCreate(r, inviteResource(created.Invite.ID), telemetry.AuditSuccess, "created")
	writeJSON(w, r, s.logger, http.StatusCreated, createInviteResponse{
		Invite: inviteDTO(created.Invite, s.clockNow()), Code: created.Code,
		AcceptPath: "/invite/" + created.Code,
	})
}

func (s *Server) decodeCreateInvite(w http.ResponseWriter, r *http.Request) (createInviteRequest, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return createInviteRequest{}, false
	}
	request, err := httputil.DecodeJSON[createInviteRequest](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid JSON object"}})
		return createInviteRequest{}, false
	}
	fields := validateCreateInviteRequest(request, s.clockNow())
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return createInviteRequest{}, false
	}
	return request, true
}

func validateCreateInviteRequest(request createInviteRequest, now time.Time) []httputil.FieldError {
	fields := make([]httputil.FieldError, 0, 4)
	if !core.ValidID(request.MediaServerID) {
		fields = append(fields, httputil.FieldError{Field: "media_server_id", Code: "invalid", Message: "must be a valid UUID"})
	}
	if err := core.ValidateInviteLabel(request.Label); err != nil {
		fields = append(fields, httputil.FieldError{Field: "label", Code: "invalid", Message: "must be 1 through 100 bytes without surrounding whitespace or control characters"})
	}
	if request.ExpiresAt != nil && !request.ExpiresAt.After(now) {
		fields = append(fields, httputil.FieldError{Field: "expires_at", Code: "invalid", Message: "must be in the future"})
	}
	if request.MaxUses != nil && (*request.MaxUses < 1 || *request.MaxUses > core.MaxInviteUses) {
		fields = append(fields, httputil.FieldError{Field: "max_uses", Code: "invalid", Message: "must be 1 through 1000"})
	}
	return fields
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	after, size, fields := invitePageParams(r)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	invites, err := s.inviteReader.List(r.Context(), after, size+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	invites, cursor := invitePage(invites, size)
	items := make([]inviteResponse, 0, len(invites))
	now := s.clockNow()
	for _, value := range invites {
		items = append(items, inviteDTO(value, now))
	}
	writeJSON(w, r, s.logger, http.StatusOK, invitesResponse{Items: items, NextCursor: cursor})
}

func (s *Server) handleGetInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := inviteID(w, r, s)
	if !ok {
		return
	}
	value, err := s.inviteReader.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, inviteDTO(value, s.clockNow()))
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := inviteID(w, r, s)
	if !ok {
		return
	}
	value, err := s.inviteManager.Revoke(r.Context(), id)
	if err != nil {
		s.emitInviteAudit(r, "invite.revoke", inviteResource(id), telemetry.AuditFailure, inviteFailureReason(err), true)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitInviteAudit(r, "invite.revoke", inviteResource(value.ID), telemetry.AuditSuccess, "revoked", true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePreviewInvite(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !s.allowPublicInvite(w, r, code, false) {
		return
	}
	preview, err := s.inviteReader.Preview(r.Context(), code)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, publicInviteResponse{
		MediaServerName: preview.MediaServerName, UsernameRule: usernameRule, PasswordRule: passwordRule,
	})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !s.allowPublicInvite(w, r, code, true) {
		return
	}
	request, ok := s.decodeAcceptInvite(w, r)
	if !ok {
		s.recordInviteAcceptFailure(r, "invalid_input")
		return
	}
	if !s.acquireInviteAcceptance() {
		s.writeInviteBusy(w, r)
		return
	}
	defer s.releaseInviteAcceptance()
	ctx, cancel := context.WithTimeout(r.Context(), s.mediaOperationTimeout)
	defer cancel()
	account, _ := accountFrom(r.Context())
	accepted, err := s.inviteManager.Accept(ctx, account.ID, code, request.Username, request.Password)
	if err != nil {
		s.recordInviteAcceptFailure(r, inviteFailureReason(err))
		s.writeMediaServerError(w, r, err)
		return
	}
	s.recordInviteAcceptResource(r, inviteResource(accepted.InviteID), telemetry.AuditSuccess, "accepted")
	writeJSON(w, r, s.logger, http.StatusCreated, acceptInviteResponse{
		MediaServerName: accepted.MediaServerName, Username: accepted.Username,
	})
}

func (s *Server) decodeAcceptInvite(w http.ResponseWriter, r *http.Request) (acceptInviteRequest, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return acceptInviteRequest{}, false
	}
	request, err := httputil.DecodeJSON[acceptInviteRequest](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid JSON object"}})
		return acceptInviteRequest{}, false
	}
	fields := validateAcceptInviteRequest(request)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return acceptInviteRequest{}, false
	}
	return request, true
}

func validateAcceptInviteRequest(request acceptInviteRequest) []httputil.FieldError {
	fields := make([]httputil.FieldError, 0, 2)
	if err := core.ValidateJellyfinUsername(request.Username); err != nil {
		fields = append(fields, httputil.FieldError{Field: "username", Code: "invalid", Message: usernameRule})
	}
	if err := core.ValidateNewPassword(request.Password); err != nil {
		fields = append(fields, httputil.FieldError{Field: "password", Code: "invalid", Message: passwordRule})
	}
	return fields
}

func (s *Server) allowPublicInvite(w http.ResponseWriter, r *http.Request, code string, acceptance bool) bool {
	ip := clientIP(r, s.trustedProxyCIDRs)
	digest := sha256.Sum256([]byte(code))
	allowed, retry := s.inviteLimiter.allow("invite-ip:"+ip, "invite-code:"+hex.EncodeToString(digest[:]))
	if allowed {
		return true
	}
	seconds := max(1, int((retry+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	if acceptance {
		s.recordInviteAcceptFailure(r, "rate_limited")
	}
	writeError(w, r, s.logger, errRateLimited)
	return false
}

func (s *Server) acquireInviteAcceptance() bool {
	select {
	case s.inviteAcceptances <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseInviteAcceptance() { <-s.inviteAcceptances }

func (s *Server) writeInviteBusy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "1")
	s.recordInviteAcceptFailure(r, "overloaded")
	writeError(w, r, s.logger, errAuthenticationBusy)
}

func (s *Server) recordInviteCreate(
	r *http.Request, resource string, result telemetry.AuditResult, reason string,
) {
	outcome := "failure"
	if result == telemetry.AuditSuccess {
		outcome = "success"
	}
	s.inviteMetrics.IncInviteCreation(outcome)
	s.emitInviteAudit(r, "invite.create", resource, result, reason, true)
}

func (s *Server) recordInviteAcceptFailure(r *http.Request, reason string) {
	s.recordInviteAcceptResource(r, "invite:unresolved", telemetry.AuditFailure, reason)
}

func (s *Server) recordInviteAcceptResource(
	r *http.Request, resource string, result telemetry.AuditResult, reason string,
) {
	s.inviteMetrics.IncInviteAcceptance(reason)
	_, authenticated := accountFrom(r.Context())
	s.emitInviteAudit(r, "invite.accept", resource, result, reason, authenticated)
}

func (s *Server) emitInviteAudit(
	r *http.Request, action, resource string, result telemetry.AuditResult, reason string, authenticated bool,
) {
	actor := "anonymous"
	if authenticated {
		account, _ := accountFrom(r.Context())
		actor = account.ID
	}
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: actor, Action: action, Resource: resource, Result: result, Reason: reason,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}

func inviteFailureReason(err error) string {
	var nameErr *core.MediaUserNameError
	switch {
	case errors.Is(err, core.ErrInviteProvisioningPending):
		return "provisioning_cleanup_pending"
	case errors.As(err, &nameErr):
		return "username_unavailable"
	case errors.Is(err, core.ErrInviteUnavailable):
		return "unavailable"
	case errors.Is(err, core.ErrInvalidArgument), errors.Is(err, core.ErrPasswordTooShort),
		errors.Is(err, core.ErrPasswordTooLong), errors.Is(err, core.ErrPasswordCompromised):
		return "invalid_input"
	case isMediaServerError(err):
		return "media_server_failure"
	default:
		return "internal_error"
	}
}

func inviteDTO(value core.Invite, now time.Time) inviteResponse {
	return inviteResponse{
		ID: value.ID, MediaServerID: value.MediaServerID, Label: value.Label,
		ExpiresAt: value.ExpiresAt, MaxUses: value.MaxUses, UseCount: value.UseCount,
		LibraryIDs: append([]string{}, value.LibraryIDs...), Status: value.Status(now), CreatedAt: value.CreatedAt,
	}
}

func inviteID(w http.ResponseWriter, r *http.Request, s *Server) (string, bool) {
	id := r.PathValue("id")
	if core.ValidID(id) {
		return id, true
	}
	s.writeValidation(w, r, []httputil.FieldError{{Field: "id", Code: "invalid", Message: "must be a valid UUID"}})
	return "", false
}

func invitePageParams(r *http.Request) (*core.InviteCursor, int, []httputil.FieldError) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, 0, []httputil.FieldError{{Field: "query", Code: "invalid", Message: "must be valid URL query encoding"}}
	}
	size, sizeErr := parseMediaServerPageSize(query["page_size"])
	cursor, cursorErr := parseInviteCursor(query["cursor"])
	fields := make([]httputil.FieldError, 0, 2)
	if sizeErr != "" {
		fields = append(fields, httputil.FieldError{Field: "page_size", Code: "invalid", Message: sizeErr})
	}
	if cursorErr != "" {
		fields = append(fields, httputil.FieldError{Field: "cursor", Code: "invalid", Message: cursorErr})
	}
	return cursor, min(size, maxInvitePageSize), fields
}

func parseInviteCursor(values []string) (*core.InviteCursor, string) {
	if len(values) == 0 {
		return nil, ""
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxInviteCursorBytes {
		return nil, "must be one valid cursor"
	}
	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	created, id, found := strings.Cut(string(decoded), "|")
	instant, timeErr := time.Parse(time.RFC3339Nano, created)
	if err != nil || timeErr != nil || !found || !core.ValidID(id) {
		return nil, "must be one valid cursor"
	}
	return &core.InviteCursor{CreatedAt: instant, ID: id}, ""
}

func invitePage(values []core.Invite, pageSize int) ([]core.Invite, string) {
	if len(values) <= pageSize {
		return values, ""
	}
	page := values[:pageSize]
	last := page[len(page)-1]
	raw := last.CreatedAt.Format(time.RFC3339Nano) + "|" + last.ID
	return page, base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func inviteResource(id string) string { return "invite:" + id }
