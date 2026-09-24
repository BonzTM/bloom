// Package playback owns per-server polling and lifecycle orchestration.
package playback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	minActiveInterval = time.Second
	maxActiveInterval = time.Minute
	minIdleInterval   = 5 * time.Second
	maxIdleInterval   = 10 * time.Minute
	minStoreTimeout   = 100 * time.Millisecond
	maxStoreTimeout   = 30 * time.Second
	maxFailureBackoff = time.Minute
)

// Source lists one server's active playback sessions.
type Source interface {
	ListSessions(ctx context.Context) ([]core.PlaybackSession, error)
}

// SourceFunc adapts a function to Source.
type SourceFunc func(context.Context) ([]core.PlaybackSession, error)

// ListSessions calls f.
func (f SourceFunc) ListSessions(ctx context.Context) ([]core.PlaybackSession, error) { return f(ctx) }

// Observer records bounded playback collector metrics.
type Observer interface {
	ObservePlaybackPoll(kind, outcome string, seconds float64)
	SetOpenWatches(kind, serverID string, count int)
	IncWatchesClosed(kind, reason string)
	IncLibraryResolution(serverID, outcome string)
}

// Config is the validated collector configuration.
type Config struct {
	ActiveInterval time.Duration
	IdleInterval   time.Duration
	MissedPolls    int
	ResumeWindow   time.Duration
	StoreTimeout   time.Duration
}

// Dependencies are injected so polling tests need no wall-clock sleeps.
type Dependencies struct {
	Store       core.PlaybackPersistence
	Source      Source
	Clock       core.Clock
	Logger      *slog.Logger
	Observer    Observer
	Wait        func(context.Context, time.Duration) error
	RandomInt64 func(int64) int64
	NewID       func() (string, error)
}

// Collector polls and persists playback for one media server.
type Collector struct {
	server     core.MediaServer
	config     Config
	deps       Dependencies
	tracker    *core.PlaybackTracker
	resolution *libraryResolverWorker
}

// NewCollector validates dependencies and returns one per-server collector.
func NewCollector(server core.MediaServer, config Config, deps Dependencies) (*Collector, error) {
	if !core.ValidID(server.ID) || !server.Kind.Valid() || !validConfig(config) ||
		deps.Store == nil || deps.Source == nil || deps.Clock == nil || deps.Logger == nil {
		return nil, fmt.Errorf("playback collector: %w", core.ErrInvalidArgument)
	}
	if deps.Wait == nil {
		deps.Wait = waitContext
	}
	if deps.RandomInt64 == nil {
		deps.RandomInt64 = rand.Int64N
	}
	if deps.NewID == nil {
		deps.NewID = core.NewID
	}
	collector := &Collector{server: server, config: config, deps: deps}
	if resolver, ok := deps.Source.(core.LibraryResolver); ok {
		collector.resolution = newLibraryResolverWorker(server, config, deps, resolver)
	}
	return collector, nil
}

func validConfig(config Config) bool {
	return config.ActiveInterval >= minActiveInterval && config.ActiveInterval <= maxActiveInterval &&
		config.IdleInterval >= minIdleInterval && config.IdleInterval <= maxIdleInterval &&
		config.MissedPolls > 0 &&
		config.MissedPolls <= core.MaxRestoredPlaybackWatches/core.MaxPlaybackSessions &&
		config.ResumeWindow > 0 &&
		config.StoreTimeout >= minStoreTimeout && config.StoreTimeout <= maxStoreTimeout
}

// Run owns library resolution, polls sequentially, and joins all work on exit.
func (c *Collector) Run(ctx context.Context) error {
	cancelResolver, resolverDone := c.startResolver(ctx)
	if cancelResolver != nil {
		defer func() {
			cancelResolver()
			<-resolverDone
		}()
	}
	return c.runPollLoop(ctx)
}

func (c *Collector) startResolver(ctx context.Context) (context.CancelFunc, <-chan struct{}) {
	if c.resolution == nil {
		return nil, nil
	}
	resolverCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.resolution.run(resolverCtx)
	}()
	return cancel, done
}

func (c *Collector) runPollLoop(ctx context.Context) error {
	if c.deps.Observer != nil {
		defer c.deps.Observer.SetOpenWatches(string(c.server.Kind), c.server.ID, 0)
	}
	failures := 0
	for {
		_, err := c.runOnce(ctx)
		if ctx.Err() != nil &&
			(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return nil
		}
		if errors.Is(err, core.ErrNotFound) {
			return nil
		}
		if err != nil {
			failures++
			c.deps.Logger.WarnContext(ctx, "playback poll failed",
				"media_server_id", c.server.ID, "kind", c.server.Kind, "error", err)
		} else {
			failures = 0
		}
		delay := c.nextDelay(failures)
		if waitErr := c.deps.Wait(ctx, delay); waitErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wait for playback poll: %w", waitErr)
		}
	}
}

func (c *Collector) runOnce(ctx context.Context) (string, error) {
	started := c.deps.Clock.Now()
	if err := c.initialize(ctx); err != nil {
		c.observePoll("failure", started)
		return "store_failure", err
	}
	return c.poll(ctx)
}

func (c *Collector) initialize(ctx context.Context) error {
	if c.tracker != nil {
		return nil
	}
	storeCtx, cancel := c.storeContext(ctx)
	openWatches, err := c.deps.Store.LoadOpenWatches(storeCtx, c.server.ID)
	cancel()
	if err != nil {
		return fmt.Errorf("load open playback watches for %s: %w", c.server.ID, err)
	}
	now := core.NormalizeTime(c.deps.Clock.Now())
	tracker, err := core.NewPlaybackTracker(c.server.ID, core.PlaybackTrackerConfig{
		MissedPolls: c.config.MissedPolls, ResumeWindow: c.config.ResumeWindow,
	}, openWatches, nil)
	if err != nil {
		return fmt.Errorf("restore playback tracker for %s: %w", c.server.ID, err)
	}
	c.tracker = tracker
	overflow := tracker.CloseOverflow(core.MaxPlaybackSessions * c.config.MissedPolls)
	if len(overflow) > 0 {
		c.deps.Logger.WarnContext(ctx, "closed playback restore overflow",
			"media_server_id", c.server.ID, "kind", c.server.Kind, "count", len(overflow))
	}
	budget := time.Duration(c.config.MissedPolls) * c.config.ActiveInterval
	mutations := make([]core.PlaybackMutation, 0, len(overflow)+tracker.OpenCount())
	mutations = append(mutations, overflow...)
	mutations = append(mutations, tracker.CloseStale(now, budget)...)
	if err := c.saveMutations(ctx, mutations); err != nil {
		c.tracker = nil
		return err
	}
	c.observeOpen()
	return nil
}

func (c *Collector) poll(ctx context.Context) (string, error) {
	started := c.deps.Clock.Now()
	sessions, err := c.deps.Source.ListSessions(ctx)
	if err != nil {
		c.observePoll("failure", started)
		return "source_failure", fmt.Errorf("list playback sessions: %w", err)
	}
	now := core.NormalizeTime(c.deps.Clock.Now())
	if restoreErr := c.restoreRecent(ctx, now, sessions); restoreErr != nil {
		c.observePoll("failure", started)
		return "store_failure", restoreErr
	}
	mutations, err := c.tracker.Observe(now, core.WatchSourcePoll, sessions, c.deps.NewID)
	if err != nil {
		c.observePoll("failure", started)
		return "store_failure", fmt.Errorf("apply playback sessions: %w", err)
	}
	if err := c.saveMutations(ctx, mutations); err != nil {
		c.tracker = nil
		c.observePoll("failure", started)
		return "store_failure", err
	}
	c.observePoll("success", started)
	c.observeOpen()
	if c.resolution != nil {
		c.resolution.enqueueForeground(mutations)
		c.resolution.requestBackfill()
	}
	return "success", nil
}

func (c *Collector) restoreRecent(
	ctx context.Context,
	now time.Time,
	sessions []core.PlaybackSession,
) error {
	for _, session := range sessions {
		open, err := c.tracker.HasOpen(session)
		if err != nil {
			return fmt.Errorf("resolve playback session: %w", err)
		}
		if open {
			continue
		}
		key := core.PlaybackKey{
			MediaServerID: c.server.ID, MediaUserID: session.MediaUserID,
			DeviceID: session.DeviceID, ItemID: session.ItemID,
			ServerSessionID: session.ServerSessionID,
		}
		storeCtx, cancel := c.storeContext(ctx)
		watches, err := c.deps.Store.ListWatches(storeCtx, core.PlaybackQuery{
			Mode: core.PlaybackQueryRecent, Key: key,
			EndedAfter: now.Add(-c.config.ResumeWindow), PageSize: 1,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("find recent playback watch: %w", err)
		}
		if len(watches) == 1 {
			if err := c.tracker.RestoreRecent(watches[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Collector) saveMutations(ctx context.Context, mutations []core.PlaybackMutation) error {
	for start := 0; start < len(mutations); start += core.MaxPlaybackMutations {
		end := min(start+core.MaxPlaybackMutations, len(mutations))
		storeCtx, cancel := c.storeContext(ctx)
		err := c.deps.Store.SaveWatches(storeCtx, mutations[start:end])
		cancel()
		if err != nil {
			return fmt.Errorf("persist playback watches: %w", err)
		}
		for _, mutation := range mutations[start:end] {
			if mutation.CloseReason != "" && c.deps.Observer != nil {
				c.deps.Observer.IncWatchesClosed(string(c.server.Kind), mutation.CloseReason)
			}
		}
	}
	return nil
}

func (c *Collector) storeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.config.StoreTimeout)
}

func (c *Collector) nextDelay(failures int) time.Duration {
	if failures > 0 {
		exponent := min(failures-1, 6)
		maximum := min(time.Second*time.Duration(1<<exponent), maxFailureBackoff)
		return fullJitter(maximum, c.deps.RandomInt64)
	}
	if c.tracker.OpenCount() > 0 {
		return intervalJitter(c.config.ActiveInterval, minActiveInterval, maxActiveInterval, c.deps.RandomInt64)
	}
	return intervalJitter(c.config.IdleInterval, minIdleInterval, maxIdleInterval, c.deps.RandomInt64)
}

func intervalJitter(base, lower, upper time.Duration, randomInt64 func(int64) int64) time.Duration {
	span := base / 5
	if span <= 0 {
		return min(max(base, lower), upper)
	}
	delta := time.Duration(randomInt64(int64(span)+1)) - span/2
	return min(max(base+delta, lower), upper)
}

func fullJitter(maximum time.Duration, randomInt64 func(int64) int64) time.Duration {
	return time.Duration(randomInt64(int64(maximum) + 1))
}

func (c *Collector) observePoll(outcome string, started time.Time) {
	if c.deps.Observer == nil {
		return
	}
	elapsed := c.deps.Clock.Now().Sub(started)
	c.deps.Observer.ObservePlaybackPoll(string(c.server.Kind), outcome, max(elapsed.Seconds(), 0))
}

func (c *Collector) observeOpen() {
	if c.deps.Observer != nil {
		c.deps.Observer.SetOpenWatches(string(c.server.Kind), c.server.ID, c.tracker.OpenCount())
	}
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
