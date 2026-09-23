package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

type retryRoundTripper struct {
	statuses []int
	calls    int
}

func (t *retryRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	status := t.statuses[t.calls]
	t.calls++
	response := &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("retry")),
	}
	if status == http.StatusTooManyRequests {
		response.Header.Set("Retry-After", "4")
	}
	return response, nil
}

type retryClock struct{ now time.Time }

func (c *retryClock) Now() time.Time { return c.now }

type deadlineContext struct {
	context.Context
	deadline time.Time
}

func (c deadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

type errorRoundTripper struct {
	err   error
	calls int
}

func (t *errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, t.err
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestRetryTransportClassifiesNetworkErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		attempts int
	}{
		{name: "timeout", err: timeoutError{}, attempts: 3},
		{name: "connection refused", err: syscall.ECONNREFUSED, attempts: 3},
		{name: "connection reset", err: syscall.ECONNRESET, attempts: 3},
		{name: "EOF", err: io.EOF, attempts: 3},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, attempts: 3},
		{name: "temporary DNS", err: &net.DNSError{Err: "temporary", IsTemporary: true}, attempts: 3},
		{name: "terminal DNS", err: &net.DNSError{Err: "not found"}, attempts: 1},
		{name: "TLS protocol", err: tls.RecordHeaderError{Msg: "malformed"}, attempts: 1},
		{name: "certificate", err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, attempts: 1},
		{name: "malformed request", err: errors.New("invalid request shape"), attempts: 1},
		{name: "redirect policy", err: &redirectPolicyError{}, attempts: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			base := &errorRoundTripper{err: testCase.err}
			policy := retryPolicy{
				clock: &retryClock{}, random: func(time.Duration) (time.Duration, error) { return 0, nil },
				wait: func(context.Context, time.Duration) error { return nil },
			}
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://id.example", nil)
			if err != nil {
				t.Fatalf("NewRequestWithContext: %v", err)
			}
			_, roundTripErr := (retryTransport{
				base: base, operation: "discovery", metrics: nopMetrics{}, policy: policy,
			}).RoundTrip(request)
			if roundTripErr == nil {
				t.Fatal("RoundTrip succeeded, want transport error")
			}
			if base.calls != testCase.attempts {
				t.Fatalf("attempts = %d, want %d", base.calls, testCase.attempts)
			}
		})
	}
}

func TestRetryTransportAttemptsAndDelays(t *testing.T) {
	clock := &retryClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	base := &retryRoundTripper{statuses: []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusOK}}
	var delays []time.Duration
	policy := retryPolicy{
		clock: clock,
		random: func(limit time.Duration) (time.Duration, error) {
			return limit / 2, nil
		},
		wait: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			clock.now = clock.now.Add(delay)
			return nil
		},
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://id.example", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	response, err := (retryTransport{base: base, operation: "discovery", metrics: nopMetrics{}, policy: policy}).RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if base.calls != 3 {
		t.Fatalf("attempts = %d, want 3", base.calls)
	}
	if got := len(delays); got != 2 || delays[0] != 50*time.Millisecond || delays[1] != 4*time.Second {
		t.Fatalf("delays = %v, want [50ms 4s]", delays)
	}
}

func TestRetryPolicyCapsRetryAfterAndDeadline(t *testing.T) {
	clock := &retryClock{now: time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)}
	policy := retryPolicy{clock: clock}
	response := &http.Response{Header: make(http.Header)}
	response.Header.Set("Retry-After", "120")
	if got, err := policy.delay(context.Background(), response, 1); err != nil || got != maxRetryDelay {
		t.Fatalf("capped Retry-After = %s, %v; want %s", got, err, maxRetryDelay)
	}
	ctx := deadlineContext{Context: context.Background(), deadline: clock.now.Add(2 * time.Second)}
	if got, err := policy.delay(ctx, response, 1); err != nil || got != 2*time.Second {
		t.Fatalf("deadline-capped Retry-After = %s, %v; want 2s", got, err)
	}
}

func TestRetryTransportWaitCancellationStopsAttempts(t *testing.T) {
	base := &retryRoundTripper{statuses: []int{http.StatusServiceUnavailable}}
	ctx, cancel := context.WithCancel(context.Background())
	waitStarted := make(chan struct{}, 1)
	policy := retryPolicy{
		clock:  &retryClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		random: func(time.Duration) (time.Duration, error) { return time.Millisecond, nil },
		wait: func(waitCtx context.Context, _ time.Duration) error {
			waitStarted <- struct{}{}
			<-waitCtx.Done()
			return waitCtx.Err()
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://id.example", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, retryErr := (retryTransport{base: base, operation: "jwks", metrics: nopMetrics{}, policy: policy}).RoundTrip(request)
		done <- retryErr
	}()
	<-waitStarted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || base.calls != 1 {
		t.Fatalf("RoundTrip = %v after %d attempts, want cancellation after one attempt", err, base.calls)
	}
}

func TestRetryPolicyCapsJitter(t *testing.T) {
	clock := &retryClock{now: time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)}
	var requested time.Duration
	policy := retryPolicy{
		clock: clock,
		random: func(limit time.Duration) (time.Duration, error) {
			requested = limit
			return limit, nil
		},
	}
	ctx := deadlineContext{Context: context.Background(), deadline: clock.now.Add(2 * time.Second)}
	got, err := policy.delay(ctx, nil, 10)
	if err != nil || requested != maxRetryDelay || got != 2*time.Second {
		t.Fatalf("jitter cap = requested %s delay %s error %v; want %s then 2s", requested, got, err, maxRetryDelay)
	}
}

func TestRetryPolicyReportsRandomFailure(t *testing.T) {
	want := errors.New("random unavailable")
	policy := retryPolicy{
		clock:  &retryClock{},
		random: func(time.Duration) (time.Duration, error) { return 0, want },
	}
	if _, err := policy.delay(context.Background(), nil, 1); !errors.Is(err, want) {
		t.Fatalf("delay error = %v, want random failure", err)
	}
}
