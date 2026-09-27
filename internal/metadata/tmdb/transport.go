package tmdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type retryTransport struct {
	next           http.RoundTripper
	metrics        Metrics
	clock          core.Clock
	wait           func(context.Context, time.Duration) error
	randomInt64N   func(int64) int64
	limiter        *tokenBucket
	attemptTimeout time.Duration
}

func (t *retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	operation := operationFromPath(request.URL.Path)
	for attempt := range maxAttempts {
		if err := t.limiter.Wait(request.Context(), t.wait); err != nil {
			return nil, err
		}
		response, err := t.roundTripAttempt(request)
		if !shouldRetry(response, err, attempt) {
			if response != nil {
				response.Body = http.MaxBytesReader(nil, response.Body, maxResponseBytes)
			}
			return response, err
		}
		delay := t.retryDelay(response, attempt)
		if !retryFits(request.Context(), delay, t.attemptTimeout) {
			if response != nil {
				response.Body = http.MaxBytesReader(nil, response.Body, maxResponseBytes)
			}
			return response, err
		}
		drainAndClose(response)
		t.metrics.ObserveMetadataRetry("tmdb", operation, "retry")
		if err := t.wait(request.Context(), delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("tmdb retry loop exhausted: %w", core.ErrMetadataUnavailable)
}

func (t *retryTransport) roundTripAttempt(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(request.Context(), t.attemptTimeout)
	response, err := t.next.RoundTrip(request.Clone(ctx))
	if err != nil {
		cancel()
		return response, err
	}
	if response == nil {
		cancel()
		return nil, errors.New("tmdb transport returned no response")
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func retryFits(ctx context.Context, delay, timeout time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) >= delay+timeout
}

func shouldRetry(response *http.Response, err error, attempt int) bool {
	if attempt+1 >= maxAttempts {
		return false
	}
	return retryableError(err) || response != nil && retryableStatus(response.StatusCode)
}

func (t *retryTransport) retryDelay(response *http.Response, attempt int) time.Duration {
	if delay := retryAfter(response); delay > 0 {
		return delay
	}
	maximum := time.Duration(1<<attempt) * 100 * time.Millisecond
	return time.Duration(t.randomInt64N(int64(maximum) + 1))
}

func drainAndClose(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	if _, err := io.CopyN(io.Discard, response.Body, maxResponseBytes); err != nil && !errors.Is(err, io.EOF) {
		_ = response.Body.Close()
		return
	}
	_ = response.Body.Close()
}

func operationFromPath(path string) string {
	switch {
	case path == "/3/authentication":
		return "probe"
	case path == "/3/search/multi":
		return "search"
	case path == "/3/trending/all/week":
		return "discover_trending"
	case path == "/3/movie/popular":
		return "discover_movies_popular"
	case path == "/3/tv/popular":
		return "discover_series_popular"
	case path == "/3/movie/upcoming":
		return "discover_movies_upcoming"
	case path == "/3/tv/on_the_air":
		return "discover_series_upcoming"
	case path == "/3/genre/movie/list":
		return "genres_movie"
	case path == "/3/genre/tv/list":
		return "genres_series"
	case len(path) > len("/3/movie/") && path[:len("/3/movie/")] == "/3/movie/":
		return "movie"
	case len(path) > len("/3/tv/") && path[:len("/3/tv/")] == "/3/tv/":
		return "series"
	default:
		return "unknown"
	}
}

type tokenBucket struct {
	mu     sync.Mutex
	clock  core.Clock
	tokens float64
	last   time.Time
	rate   float64
	burst  float64
}

func newTokenBucket(clock core.Clock, rate, burst int) *tokenBucket {
	now := clock.Now()
	return &tokenBucket{clock: clock, tokens: float64(burst), last: now, rate: float64(rate), burst: float64(burst)}
}

func (b *tokenBucket) Wait(ctx context.Context, wait func(context.Context, time.Duration) error) error {
	for range 2 {
		delay := b.reserve()
		if delay <= 0 {
			return nil
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
	return fmt.Errorf("tmdb rate limiter failed to admit: %w", core.ErrMetadataUnavailable)
}

func (b *tokenBucket) reserve() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
}
