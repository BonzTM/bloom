// Package stats coordinates cached statistics reads.
package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const resultCacheCapacity = 256

// Observer records low-cardinality statistics query duration.
type Observer interface {
	ObserveStatsQuery(report, outcome string, seconds float64)
}

// Service provides bounded, briefly cached dashboard reads.
type Service struct {
	reader  core.StatsReader
	clock   core.Clock
	metrics Observer
	cache   *resultCache
}

var _ core.StatsReader = (*Service)(nil)

// NewService validates dependencies and creates a bounded result cache.
func NewService(reader core.StatsReader, clock core.Clock, ttl time.Duration, metrics Observer) (*Service, error) {
	if reader == nil || clock == nil || metrics == nil || ttl < 0 {
		return nil, fmt.Errorf("statistics service: %w", core.ErrInvalidArgument)
	}
	return &Service{
		reader: reader, clock: clock, metrics: metrics,
		cache: newResultCache(clock, resultCacheCapacity, ttl),
	}, nil
}

// ReadStats returns one report, using TTL expiry as the invalidation policy.
func (s *Service) ReadStats(ctx context.Context, query core.StatsQuery) (core.StatsResult, error) {
	if err := query.Validate(); err != nil {
		return core.StatsResult{}, fmt.Errorf("statistics service read: %w", err)
	}
	key := resultCacheKey(query)
	if result, ok := s.cache.get(key); ok {
		return result, nil
	}
	started := s.clock.Now()
	result, err := s.reader.ReadStats(ctx, query)
	outcome := "success"
	if err != nil {
		outcome = "error"
	} else {
		s.cache.put(key, result)
	}
	s.metrics.ObserveStatsQuery(string(query.Report), outcome, s.clock.Now().Sub(started).Seconds())
	if err != nil {
		return core.StatsResult{}, fmt.Errorf("statistics service read: %w", err)
	}
	return result, nil
}
