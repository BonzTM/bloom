package runtime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/playback"
)

type transientRuntimeServerLister struct {
	attempts chan int32
	calls    atomic.Int32
	err      error
}

func (l *transientRuntimeServerLister) List(
	context.Context, string, int,
) ([]core.MediaServerConnection, error) {
	call := l.calls.Add(1)
	l.attempts <- call
	if call == 1 {
		return nil, l.err
	}
	return nil, nil
}

func TestRunStaysLiveWhenInitialPlaybackListFails(t *testing.T) {
	cfg := cleanupTestConfig(t)
	resources := &startupResources{tracer: &recordingTracer{}}
	deps := cleanupTestDependencies(resources)
	refresh := make(chan time.Time, 1)
	lister := &transientRuntimeServerLister{
		attempts: make(chan int32, 2), err: errors.New("database unavailable"),
	}
	deps.newPlaybackManager = func(input playbackManagerDependencies) (*playback.Manager, error) {
		return playback.NewManager(
			lister, input.store, input.config, input.factory, input.clock,
			input.logger, input.metrics, playback.ManagerOptions{Refresh: refresh},
		)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	ready := make(chan net.Addr, 1)
	deps.ListenerReady = func(addr net.Addr) { ready <- addr }
	go func() { done <- Run(ctx, cfg, Streams{Log: io.Discard, Audit: io.Discard}, deps) }()

	addr := awaitRuntimePlaybackReady(t, ready, done)
	awaitRuntimePlaybackAttempt(t, lister.attempts, done, 1)
	refresh <- time.Time{}
	awaitRuntimePlaybackAttempt(t, lister.attempts, done, 2)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr.String()+"/livez", nil)
	if err != nil {
		t.Fatalf("build GET /livez: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET /livez after playback list recovery: %v", err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatalf("close /livez response: %v", closeErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /livez = %d, want %d", response.StatusCode, http.StatusOK)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
}

func awaitRuntimePlaybackReady(t *testing.T, ready <-chan net.Addr, done <-chan error) net.Addr {
	t.Helper()
	select {
	case addr := <-ready:
		return addr
	case err := <-done:
		t.Fatalf("Run stopped before readiness: %v", err)
		return nil
	}
}

func awaitRuntimePlaybackAttempt(t *testing.T, attempts <-chan int32, done <-chan error, want int32) {
	t.Helper()
	select {
	case got := <-attempts:
		if got != want {
			t.Fatalf("playback list attempt = %d, want %d", got, want)
		}
	case err := <-done:
		t.Fatalf("Run stopped before playback list attempt %d: %v", want, err)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for playback list attempt %d", want)
	}
}
