package http

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	// Scan limits bound malformed raw headers; exhaustion fails closed as an inbound cookie.
	maxSessionCookieHeaderValues = 100
	maxSessionCookiePairs        = 4096
)

type sessionRequestState struct {
	inbound           bool
	resolved          bool
	clearCookie       bool
	replacementReady  bool
	afterCommit       func()
	onCommitFailed    func(error)
	authDeadline      time.Time
	privateImageCache bool
}

type bufferedResponse struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), status: http.StatusOK}
}

func (w *bufferedResponse) Header() http.Header { return w.header }

func (w *bufferedResponse) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
}

func (w *bufferedResponse) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(data)
}

func (s *Server) loadAndSaveSessions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSessionResponseHeaders(w.Header())
		ctx, state, ok := s.loadSession(w, r)
		if !ok {
			return
		}
		buffer := newBufferedResponse()
		setSessionResponseHeaders(buffer.Header())
		next.ServeHTTP(buffer, r.WithContext(ctx))
		commitContext, cancel := state.contextForCommit(ctx)
		defer cancel()
		commitRequest := r.WithContext(commitContext)
		if !s.commitSession(w, buffer, commitRequest, state) {
			return
		}
		if err := flushBufferedResponse(w, buffer); err != nil {
			s.logger.WarnContext(ctx, "write buffered response failed", "error", err.Error())
		}
	})
}

func (s *Server) loadAndStreamSessions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSessionResponseHeaders(w.Header())
		ctx, state, ok := s.loadSession(w, r)
		if !ok {
			return
		}
		response := newStreamingSessionResponse(s, w, r.WithContext(ctx), state)
		next.ServeHTTP(response, r.WithContext(ctx))
		response.finish()
	})
}

// streamingSessionResponse commits session headers before forwarding the body.
type streamingSessionResponse struct {
	server      *Server
	destination http.ResponseWriter
	request     *http.Request
	state       *sessionRequestState
	header      http.Header
	status      int
	wroteHeader bool
	committed   bool
	failed      bool
}

func newStreamingSessionResponse(
	server *Server, destination http.ResponseWriter, request *http.Request, state *sessionRequestState,
) *streamingSessionResponse {
	header := make(http.Header)
	setSessionResponseHeaders(header)
	return &streamingSessionResponse{
		server: server, destination: destination, request: request, state: state, header: header,
	}
}

func (w *streamingSessionResponse) Header() http.Header { return w.header }

func (w *streamingSessionResponse) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status, w.wroteHeader = status, true
	if !w.commit() {
		return
	}
}

func (w *streamingSessionResponse) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = http.StatusOK, true
	}
	if !w.commit() {
		return 0, errors.New("commit session before streaming response")
	}
	return w.destination.Write(data)
}

func (w *streamingSessionResponse) Unwrap() http.ResponseWriter { return w.destination }

func (w *streamingSessionResponse) finish() {
	if !w.wroteHeader {
		w.status, w.wroteHeader = http.StatusOK, true
	}
	if !w.commit() {
		return
	}
}

func (w *streamingSessionResponse) commit() bool {
	if w.committed {
		return !w.failed
	}
	w.committed = true
	ctx, cancel := w.state.contextForCommit(w.request.Context())
	defer cancel()
	if !w.server.commitSession(w.destination, w, w.request.WithContext(ctx), w.state) {
		w.failed = true
		return false
	}
	maps.Copy(w.destination.Header(), w.header)
	w.destination.WriteHeader(w.status)
	return true
}

func (s *Server) loadSession(w http.ResponseWriter, r *http.Request) (context.Context, *sessionRequestState, bool) {
	cookie, cookieErr := r.Cookie(s.sessions.Cookie.Name)
	inbound := cookieErr == nil || rawCookieNamePresent(r.Header, s.sessions.Cookie.Name)
	if cookieErr != nil && inbound {
		s.writeSessionLoadFailure(w, r, true, errAuthenticationRequired)
		return nil, nil, false
	}
	token := ""
	if inbound {
		token = cookie.Value
	}
	loadCtx := core.WithSessionLoadTracking(r.Context())
	loader := s.sessionLoader(r)
	ctx, err := loader.Load(loadCtx, token)
	if err != nil {
		s.writeSessionLoadFailure(w, r, inbound, err)
		return nil, nil, false
	}
	state := &sessionRequestState{inbound: inbound, resolved: s.sessions.Token(ctx) != ""}
	ctx = context.WithValue(ctx, sessionStateKey, state)
	return ctx, state, true
}

func rawCookieNamePresent(header http.Header, name string) bool {
	values := header.Values("Cookie")
	for valueIndex := 0; valueIndex < len(values) && valueIndex < maxSessionCookieHeaderValues; valueIndex++ {
		line := values[valueIndex]
		for pairIndex := 0; line != "" && pairIndex < maxSessionCookiePairs; pairIndex++ {
			var pair string
			pair, line, _ = strings.Cut(line, ";")
			cookieName, _, hasValue := strings.Cut(pair, "=")
			if hasValue && strings.Trim(cookieName, " \t") == name {
				return true
			}
		}
		if line != "" {
			return true
		}
	}
	return len(values) > maxSessionCookieHeaderValues
}

func (s *Server) sessionLoader(r *http.Request) *scs.SessionManager {
	if r.URL.Path != "/api/v1/auth/oidc/callback" {
		return s.sessions
	}
	// A callback may lose the atomic flow claim. Suppress the normal idle-expiry
	// touch so that loser stays unmodified and cannot emit a stale cookie.
	loader := *s.sessions
	loader.IdleTimeout = 0
	return &loader
}

func (s *Server) writeSessionLoadFailure(w http.ResponseWriter, r *http.Request, inbound bool, err error) {
	reason := "internal_error"
	if inbound && !errors.Is(err, core.ErrSessionStore) {
		reason = "malformed_session"
		s.sessions.WriteSessionCookie(r.Context(), w, "", time.Time{})
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.URL.Path == "/api/v1/auth/oidc/callback" && acceptsHTML(r.Header.Get("Accept")) {
		if reason == "malformed_session" {
			s.writeOIDCCallbackFailure(w, r, oidcErrorStateInvalid, "state_invalid", core.ErrOIDCRejected)
			return
		}
		s.writeOIDCCallbackFailure(w, r, oidcErrorInternal, "internal_error", err)
		return
	}
	s.emitAuthAudit(r, "anonymous", "auth.session", routeResource(r), telemetry.AuditFailure, reason, clientIP(r, s.trustedProxyCIDRs))
	if reason == "malformed_session" {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	writeError(w, r, s.logger, err)
}

func setSessionResponseHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Add("Vary", "Cookie")
}

func (s *Server) commitSession(
	w, sessionWriter http.ResponseWriter, r *http.Request, state *sessionRequestState,
) bool {
	if state.clearCookie && !state.replacementReady {
		s.sessions.WriteSessionCookie(r.Context(), sessionWriter, "", time.Time{})
		sessionWriter.Header().Set("Cache-Control", "no-store")
		return true
	}
	switch s.sessions.Status(r.Context()) {
	case scs.Modified:
		token, expiry, err := s.sessions.Commit(r.Context())
		if err != nil {
			if state.onCommitFailed != nil {
				state.onCommitFailed(err)
			}
			if r.URL.Path == "/api/v1/auth/oidc/callback" && acceptsHTML(r.Header.Get("Accept")) {
				s.writeOIDCBrowserRedirect(w, oidcErrorInternal)
				return false
			}
			writeError(w, r, s.logger, err)
			return false
		}
		s.sessions.WriteSessionCookie(r.Context(), sessionWriter, token, expiry)
	case scs.Destroyed:
		s.sessions.WriteSessionCookie(r.Context(), sessionWriter, "", time.Time{})
	case scs.Unmodified:
	}
	if !state.privateImageCache {
		sessionWriter.Header().Set("Cache-Control", "no-store")
	}
	if state.afterCommit != nil {
		state.afterCommit()
	}
	return true
}

func permitPrivateImageCache(ctx context.Context) {
	state := sessionState(ctx)
	if state != nil {
		state.privateImageCache = true
	}
}

func (s *sessionRequestState) contextForCommit(fallback context.Context) (context.Context, context.CancelFunc) {
	if !s.authDeadline.IsZero() {
		return context.WithDeadline(fallback, s.authDeadline)
	}
	return fallback, func() {}
}

func flushBufferedResponse(w http.ResponseWriter, buffer *bufferedResponse) error {
	maps.Copy(w.Header(), buffer.header)
	w.WriteHeader(buffer.status)
	if buffer.body.Len() == 0 {
		return nil
	}
	if _, err := w.Write(buffer.body.Bytes()); err != nil {
		return err
	}
	return nil
}

func sessionState(ctx context.Context) *sessionRequestState {
	state, ok := ctx.Value(sessionStateKey).(*sessionRequestState)
	if !ok {
		return nil
	}
	return state
}
