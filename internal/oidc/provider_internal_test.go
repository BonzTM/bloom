package oidc

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/BonzTM/bloom/internal/core"
)

type expiredDeadlineContext struct {
	context.Context
	done <-chan struct{}
}

func newExpiredDeadlineContext() context.Context {
	done := make(chan struct{})
	close(done)
	return expiredDeadlineContext{Context: context.Background(), done: done}
}

func (c expiredDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, true }
func (c expiredDeadlineContext) Done() <-chan struct{}       { return c.done }
func (c expiredDeadlineContext) Err() error                  { return context.DeadlineExceeded }

func TestClassifyVerificationDeadline(t *testing.T) {
	cause := errors.New("verification stopped")
	expired := newExpiredDeadlineContext()
	tests := []struct {
		name         string
		caller       context.Context
		verification context.Context
		unavailable  bool
		want         error
		opaque       bool
	}{
		{
			name: "caller deadline", caller: expired, verification: expired,
			unavailable: true, opaque: true,
		},
		{
			name: "independent JWKS deadline", caller: context.Background(), verification: expired,
			want: core.ErrOIDCProviderUnavailable,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			status := &verificationStatus{}
			status.unavailable.Store(testCase.unavailable)
			err := classifyVerificationError(testCase.caller, testCase.verification, status, cause)
			if testCase.opaque && (err == nil || errors.Is(err, core.ErrOIDCProviderUnavailable) || errors.Is(err, core.ErrOIDCRejected)) {
				t.Fatalf("classification = %v, want opaque internal failure", err)
			}
			if !testCase.opaque && !errors.Is(err, testCase.want) {
				t.Fatalf("classification = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestClassifyExchangeError(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name   string
		ctx    context.Context
		err    error
		want   error
		opaque bool
	}{
		{name: "connection reset", ctx: t.Context(), err: syscall.ECONNRESET, want: core.ErrOIDCProviderUnavailable},
		{name: "temporary DNS failure", ctx: t.Context(), err: &net.DNSError{Err: "temporary", IsTemporary: true}, want: core.ErrOIDCProviderUnavailable},
		{name: "provider timeout", ctx: t.Context(), err: context.DeadlineExceeded, want: core.ErrOIDCProviderUnavailable},
		{name: "token 429", ctx: t.Context(), err: retrieveError(http.StatusTooManyRequests, ""), want: core.ErrOIDCProviderUnavailable},
		{name: "token 502", ctx: t.Context(), err: retrieveError(http.StatusBadGateway, ""), want: core.ErrOIDCProviderUnavailable},
		{name: "token 503", ctx: t.Context(), err: retrieveError(http.StatusServiceUnavailable, ""), want: core.ErrOIDCProviderUnavailable},
		{name: "token 504", ctx: t.Context(), err: retrieveError(http.StatusGatewayTimeout, ""), want: core.ErrOIDCProviderUnavailable},
		{name: "invalid grant", ctx: t.Context(), err: retrieveError(http.StatusBadRequest, "invalid_grant"), want: core.ErrOIDCRejected},
		{name: "token 500", ctx: t.Context(), err: retrieveError(http.StatusInternalServerError, ""), opaque: true},
		{name: "malformed response", ctx: t.Context(), err: errors.New("decode response"), opaque: true},
		{name: "TLS failure", ctx: t.Context(), err: tls.RecordHeaderError{Msg: "malformed"}, opaque: true},
		{name: "permanent DNS failure", ctx: t.Context(), err: &net.DNSError{Err: "not found"}, opaque: true},
		{name: "request construction", ctx: t.Context(), err: errors.New("invalid request shape"), opaque: true},
		{name: "redirect rejected", ctx: t.Context(), err: &redirectPolicyError{}, opaque: true},
		{name: "caller cancellation", ctx: cancelled, err: context.Canceled, opaque: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := classifyExchangeError(testCase.ctx, testCase.err)
			if testCase.opaque && (errors.Is(err, core.ErrOIDCProviderUnavailable) || errors.Is(err, core.ErrOIDCRejected)) {
				t.Fatalf("classification = %v, want opaque internal failure", err)
			}
			if !testCase.opaque && !errors.Is(err, testCase.want) {
				t.Fatalf("classification = %v, want %v", err, testCase.want)
			}
		})
	}
}

func retrieveError(status int, code string) error {
	return &oauth2.RetrieveError{Response: &http.Response{StatusCode: status}, ErrorCode: code}
}
