package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const (
	sessionCleanupEvery       = 64
	sessionCleanupBatch       = 128
	sessionOperationLimit     = 5 * time.Second
	sessionCleanupBackoffBase = time.Second
	sessionCleanupBackoffCap  = 5 * time.Minute
)

type sessionStore struct {
	pool    *sql.DB
	driver  config.Driver
	metrics SessionCleanupMetrics
	logger  *slog.Logger
	clock   core.Clock

	cleanupMu          sync.Mutex
	cleanupCommits     uint32
	cleanupPending     bool
	cleanupRunning     bool
	cleanupFailures    uint8
	cleanupNextAttempt time.Time
}

// SessionCleanupMetrics records bounded cleanup failures without session labels.
type SessionCleanupMetrics interface {
	IncSessionCleanupFailure()
}

type nopSessionCleanupMetrics struct{}

func (nopSessionCleanupMetrics) IncSessionCleanupFailure() {}

var _ scs.CtxStore = (*sessionStore)(nil)

type oidcFlowStore struct{ pool *sql.DB }

var _ core.OIDCFlowStore = (*oidcFlowStore)(nil)

// NewOIDCFlowStore returns the atomic persistent claim boundary for OIDC flows.
func NewOIDCFlowStore(pool *sql.DB) (core.OIDCFlowStore, error) {
	if pool == nil {
		return nil, errors.New("OIDC flow store: nil database pool")
	}
	return &oidcFlowStore{pool: pool}, nil
}

func (s *oidcFlowStore) ClaimOIDCFlow(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, sessionOperationLimit)
	defer cancel()
	digest := sha256.Sum256([]byte(token))
	storedToken := base64.RawURLEncoding.EncodeToString(digest[:])
	result, err := s.pool.ExecContext(ctx, "DELETE FROM sessions WHERE token = $1", storedToken)
	if err != nil {
		return false, fmt.Errorf("claim OIDC flow: %w", errors.Join(core.ErrSessionStore, err))
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read OIDC flow claim result: %w", errors.Join(core.ErrSessionStore, err))
	}
	if rows > 1 {
		return false, fmt.Errorf("claim OIDC flow removed %d sessions: %w", rows, core.ErrSessionStore)
	}
	return rows == 1, nil
}

// NewSessionStore returns Bloom's context-aware SCS database adapter.
func NewSessionStore(
	pool *sql.DB,
	driver config.Driver,
	metrics SessionCleanupMetrics,
	logger *slog.Logger,
	clock core.Clock,
) (scs.CtxStore, error) {
	if pool == nil {
		return nil, errors.New("session store: nil database pool")
	}
	if logger == nil {
		return nil, errors.New("session store: nil logger")
	}
	if clock == nil {
		return nil, errors.New("session store: nil clock")
	}
	switch driver {
	case config.DriverSQLite, config.DriverPostgres:
		if metrics == nil {
			metrics = nopSessionCleanupMetrics{}
		}
		return &sessionStore{pool: pool, driver: driver, metrics: metrics, logger: logger, clock: clock}, nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func (s *sessionStore) Find(token string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionOperationLimit)
	defer cancel()
	return s.FindCtx(ctx, token)
}

func (s *sessionStore) Commit(token string, data []byte, expiry time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionOperationLimit)
	defer cancel()
	return s.CommitCtx(ctx, token, data, expiry)
}

func (s *sessionStore) Delete(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionOperationLimit)
	defer cancel()
	return s.DeleteCtx(ctx, token)
}

func (s *sessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionOperationLimit)
	defer cancel()
	query := "SELECT data FROM sessions WHERE token = $1 AND expiry > $2"
	var data []byte
	err := s.pool.QueryRowContext(ctx, query, token, s.expiryValue(s.clock.Now())).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find session: %w", errors.Join(core.ErrSessionStore, err))
	}
	core.MarkSessionLoaded(ctx, token)
	return data, true, nil
}

func (s *sessionStore) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, sessionOperationLimit)
	defer cancel()
	query := sessionUpsertQuery
	if core.SessionTokenWasLoaded(ctx, token) {
		query = sessionUpdateQuery
	}
	if _, err := s.pool.ExecContext(ctx, query, token, data, s.expiryValue(expiry)); err != nil {
		return fmt.Errorf("commit session: %w", errors.Join(core.ErrSessionStore, err))
	}
	s.maybeCleanup(ctx)
	return nil
}

const (
	sessionUpsertQuery = "INSERT INTO sessions (token, data, expiry) VALUES ($1, $2, $3) " +
		"ON CONFLICT (token) DO UPDATE SET data = EXCLUDED.data, expiry = EXCLUDED.expiry"
	sessionUpdateQuery = "UPDATE sessions SET data = $2, expiry = $3 WHERE token = $1"
)

func (s *sessionStore) DeleteCtx(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, sessionOperationLimit)
	defer cancel()
	if _, err := s.pool.ExecContext(ctx, "DELETE FROM sessions WHERE token = $1", token); err != nil {
		return fmt.Errorf("delete session: %w", errors.Join(core.ErrSessionStore, err))
	}
	return nil
}

func (s *sessionStore) maybeCleanup(parent context.Context) {
	if !s.beginCleanup() {
		return
	}
	err := s.cleanupExpired(parent)
	firstFailure := s.finishCleanup(err)
	if err != nil {
		s.metrics.IncSessionCleanupFailure()
	}
	if firstFailure {
		s.logger.Error("session cleanup failed", "error", err)
	}
}

func (s *sessionStore) beginCleanup() bool {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cleanupCommits < sessionCleanupEvery {
		s.cleanupCommits++
	}
	if s.cleanupRunning {
		return false
	}
	if s.cleanupPending && s.clock.Now().Before(s.cleanupNextAttempt) {
		return false
	}
	if !s.cleanupPending && s.cleanupCommits < sessionCleanupEvery {
		return false
	}
	s.cleanupRunning = true
	s.cleanupCommits = 0
	return true
}

func (s *sessionStore) finishCleanup(err error) bool {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	firstFailure := err != nil && !s.cleanupPending
	s.cleanupRunning = false
	if err == nil {
		s.cleanupPending = false
		s.cleanupFailures = 0
		s.cleanupNextAttempt = time.Time{}
		return firstFailure
	}
	s.cleanupPending = true
	if s.cleanupFailures < 255 {
		s.cleanupFailures++
	}
	s.cleanupNextAttempt = s.clock.Now().Add(sessionCleanupBackoff(s.cleanupFailures))
	return firstFailure
}

func sessionCleanupBackoff(failures uint8) time.Duration {
	if failures <= 1 {
		return sessionCleanupBackoffBase
	}
	shift := min(failures-1, 9)
	delay := sessionCleanupBackoffBase * time.Duration(1<<shift)
	return min(delay, sessionCleanupBackoffCap)
}

func (s *sessionStore) cleanupExpired(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, sessionOperationLimit)
	defer cancel()
	query := "DELETE FROM sessions WHERE token IN (" +
		"SELECT token FROM sessions WHERE expiry < $1 ORDER BY expiry LIMIT $2)"
	if _, err := s.pool.ExecContext(ctx, query, s.expiryValue(s.clock.Now()), sessionCleanupBatch); err != nil {
		return fmt.Errorf("cleanup expired sessions: %w", err)
	}
	return nil
}

func (s *sessionStore) expiryValue(expiry time.Time) any {
	normalized := core.NormalizeTime(expiry)
	if s.driver == config.DriverSQLite {
		return normalized.UnixMicro()
	}
	return normalized
}
