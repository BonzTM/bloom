package core

import (
	"context"
	"errors"
	"sync"
)

// ErrSessionStore classifies a system failure from server-side session persistence.
var ErrSessionStore = errors.New("session store failure")

type sessionLoadTrackingKey struct{}

type sessionLoadTracking struct {
	mu     sync.Mutex
	tokens map[string]struct{}
}

// WithSessionLoadTracking adds request-local state that lets a session store
// distinguish a new token from a token loaded earlier in the same request.
func WithSessionLoadTracking(ctx context.Context) context.Context {
	tracking := &sessionLoadTracking{tokens: make(map[string]struct{}, 1)}
	return context.WithValue(ctx, sessionLoadTrackingKey{}, tracking)
}

// MarkSessionLoaded records that token existed when the current request read it.
func MarkSessionLoaded(ctx context.Context, token string) {
	tracking, ok := ctx.Value(sessionLoadTrackingKey{}).(*sessionLoadTracking)
	if !ok {
		return
	}
	tracking.mu.Lock()
	defer tracking.mu.Unlock()
	tracking.tokens[token] = struct{}{}
}

// SessionTokenWasLoaded reports whether token existed earlier in this request.
func SessionTokenWasLoaded(ctx context.Context, token string) bool {
	tracking, ok := ctx.Value(sessionLoadTrackingKey{}).(*sessionLoadTracking)
	if !ok {
		return false
	}
	tracking.mu.Lock()
	defer tracking.mu.Unlock()
	_, loaded := tracking.tokens[token]
	return loaded
}
