package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/buildinfo"
	"github.com/BonzTM/bloom/internal/httputil"
)

// --- Wire DTOs -------------------------------------------------------------
//
// Dedicated transport types with explicit snake_case json tags, per the
// handbook's foundations/serialization.md. Core types are never serialized
// directly.

// versionResponse is the GET /api/v1/version body. The shape is the contract
// the frontend builds against; see api/openapi.yaml.
type versionResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// --- Handlers --------------------------------------------------------------

// handleVersion handles GET /api/v1/version: build metadata stamped through
// internal/buildinfo. It is public and read-only.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, s.logger, http.StatusOK, versionResponse{
		Name:    buildinfo.Name,
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
	})
}

// handleNotFound is the catch-all for unknown routes. It returns the structured
// JSON envelope, never HTML, so an API client that mistypes a path gets a
// response it can parse (the frontend contract).
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, s.logger, http.StatusNotFound, httputil.ErrorResponse{
		Code:      codeNotFound,
		Message:   "no such route",
		RequestID: requestIDFrom(r.Context()),
	})
}

// handleLivez reports process liveness: if this handler runs, the process is
// alive. It must NOT depend on downstream readiness, or the platform would kill
// a draining pod.
func (s *Server) handleLivez(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	s.writePlain(w, r, "ok")
}

// readyCheckTimeout bounds the database ping inside /readyz so a hung database
// cannot pin the probe past the platform's probe timeout.
const readyCheckTimeout = 2 * time.Second

// handleReadyz reports readiness to receive traffic: 200 only when the
// readiness flag is up (it flips down before shutdown drains) AND the database
// answers a ping within readyCheckTimeout; otherwise 503 with the error
// envelope. The ping failure detail goes to the boundary log, never the body.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.readyCheck(r.Context()); err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	s.writePlain(w, r, "ready")
}

// readyCheck returns nil when the service may receive traffic. Every failure
// wraps errNotReady so errorClass maps it to (503, "unavailable").
func (s *Server) readyCheck(ctx context.Context) error {
	if !s.readiness.Ready() {
		return fmt.Errorf("%w: readiness flag is down", errNotReady)
	}
	pingCtx, cancel := context.WithTimeout(ctx, readyCheckTimeout)
	defer cancel()
	if err := s.pinger.PingContext(pingCtx); err != nil {
		return fmt.Errorf("%w: database ping: %w", errNotReady, err)
	}
	return nil
}

// writePlain writes a short text probe body. The status is already committed,
// so a write error is unactionable for the client; it is logged once so a
// broken probe connection is observable rather than silently dropped.
func (s *Server) writePlain(w http.ResponseWriter, r *http.Request, body string) {
	if _, err := io.WriteString(w, body); err != nil {
		s.logger.WarnContext(r.Context(), "write probe body failed",
			"route", routePattern(r),
			"error", err.Error(),
		)
	}
}
