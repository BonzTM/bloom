package jellyfin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxAttempts   = 3
	backoffBase   = 50 * time.Millisecond
	backoffCap    = 500 * time.Millisecond
	maxRetryAfter = 30 * time.Second
)

type retryAfterError struct {
	err   *core.MediaServerError
	valid bool
}

func (e *retryAfterError) Error() string { return e.err.Error() }
func (e *retryAfterError) Unwrap() error { return e.err }

func (c *Client) getWithRetry(ctx context.Context, operation, path string) ([]byte, time.Time, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	var last error
	for attempt := range maxAttempts {
		started := c.now()
		body, retry, err := c.getAttempt(operationCtx, operation, path, started)
		if err == nil {
			return body, started, nil
		}
		last = err
		if !retry {
			return nil, time.Time{}, last
		}
		if attempt == maxAttempts-1 {
			c.observeRetry(operation, "exhausted")
			return nil, time.Time{}, last
		}
		delay := c.retryDelay(last, attempt)
		if !delayFits(operationCtx, c.now(), delay) {
			c.observeRetry(operation, "budget_exhausted")
			return nil, time.Time{}, withRetryDelay(last, delay)
		}
		c.observeRetry(operation, "scheduled")
		if err := c.sleep(operationCtx, delay); err != nil {
			return nil, time.Time{}, mediaError(operation, core.MediaServerUnavailable, err)
		}
	}
	return nil, time.Time{}, last
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func (c *Client) retryDelay(err error, attempt int) time.Duration {
	var retryAfterErr *retryAfterError
	if errors.As(err, &retryAfterErr) && retryAfterErr.valid {
		return min(retryAfterErr.err.RetryAfter, maxRetryAfter)
	}
	var mediaErr *core.MediaServerError
	if errors.As(err, &mediaErr) && mediaErr.RetryAfter > 0 {
		return min(mediaErr.RetryAfter, maxRetryAfter)
	}
	return fullJitter(backoff(attempt), c.randomInt64N)
}

func withRetryDelay(err error, delay time.Duration) error {
	var mediaErr *core.MediaServerError
	if !errors.As(err, &mediaErr) {
		return err
	}
	copy := *mediaErr
	copy.RetryAfter = min(delay, maxRetryAfter)
	return &copy
}

func delayFits(ctx context.Context, now time.Time, delay time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || !now.Add(delay).After(deadline)
}

func retryableNetworkError(parent context.Context, operation string, err error) (bool, error) {
	if parent.Err() != nil {
		return false, mediaError(operation, core.MediaServerUnavailable, parent.Err())
	}
	if terminalTLSError(err) {
		return false, mediaError(operation, core.MediaServerUnavailable, err)
	}
	retryable := transientNetworkError(err)
	return retryable, classifiedMediaError(operation, core.MediaServerUnavailable, 0, retryable, err)
}

func terminalTLSError(err error) bool {
	var verification *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var authority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verification) || errors.As(err, &record) ||
		errors.As(err, &authority) || errors.As(err, &invalid)
}

func transientNetworkError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	var dnsError *net.DNSError
	return errors.As(err, &dnsError) && dnsError.IsTemporary
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	seconds, secondsErr := strconv.ParseUint(value, 10, 64)
	if secondsErr == nil {
		capSeconds := uint64(maxRetryAfter / time.Second)
		if seconds >= capSeconds {
			return maxRetryAfter, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if errors.Is(secondsErr, strconv.ErrRange) {
		return maxRetryAfter, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return min(max(when.Sub(now), 0), maxRetryAfter), true
}

func backoff(attempt int) time.Duration {
	return min(backoffBase*time.Duration(1<<attempt), backoffCap)
}

func fullJitter(maximum time.Duration, randomInt64N func(int64) int64) time.Duration {
	if maximum <= 0 {
		return 0
	}
	return time.Duration(randomInt64N(int64(maximum) + 1))
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mediaError(operation string, kind core.MediaServerErrorKind, err error) error {
	return &core.MediaServerError{Kind: kind, Operation: operation, Err: err}
}

func classifiedMediaError(
	operation string,
	kind core.MediaServerErrorKind,
	retryAfter time.Duration,
	retryable bool,
	err error,
) error {
	return &core.MediaServerError{
		Kind: kind, Operation: operation, Retryable: retryable, RetryAfter: retryAfter, Err: err,
	}
}

func retryableResponseError(operation string, retryAfter time.Duration, valid bool) error {
	return &retryAfterError{
		err: &core.MediaServerError{
			Kind: core.MediaServerUnavailable, Operation: operation,
			Retryable: true, RetryAfter: retryAfter,
		},
		valid: valid,
	}
}

func (c *Client) observeRetry(operation, outcome string) {
	if c.observer != nil {
		c.observer.ObserveMediaServerRetry(string(core.MediaServerKindJellyfin), operation, outcome)
	}
}
