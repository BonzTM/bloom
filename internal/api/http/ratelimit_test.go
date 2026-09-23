package http

import (
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/testutil"
)

func TestLoginLimiterRefillsAndBoundsKeys(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 2, 2)

	if allowed, _ := limiter.allow("ip:one", "user:alice"); !allowed {
		t.Fatal("first attempt rejected")
	}
	if allowed, _ := limiter.allow("ip:one", "user:alice"); !allowed {
		t.Fatal("burst attempt rejected")
	}
	allowed, retry := limiter.allow("ip:one", "user:alice")
	if allowed || retry != time.Minute {
		t.Fatalf("third attempt = %v retry %s, want rejected for 1m", allowed, retry)
	}

	clock.Advance(time.Minute)
	if allowed, _ := limiter.allow("ip:one", "user:alice"); !allowed {
		t.Fatal("attempt after refill rejected")
	}

	limiter.allow("ip:two", "user:bob")
	if got := limiter.tracked(); got > 2 {
		t.Fatalf("tracked keys = %d, want <= 2", got)
	}
}

func TestLoginLimiterDoesNotConsumeOneBucketOnOtherRejection(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 1, 8)
	if allowed, _ := limiter.allow("ip:old", "user:alice"); !allowed {
		t.Fatal("initial attempt rejected")
	}
	clock.Advance(59 * time.Second)
	allowed, retry := limiter.allow("ip:fresh", "user:alice")
	if allowed || retry != time.Second {
		t.Fatalf("mixed attempt = %v, %s; want rejected for 1s", allowed, retry)
	}
	if allowed, _ := limiter.allow("ip:fresh", "user:bob"); !allowed {
		t.Fatal("fresh IP token was consumed by username rejection")
	}
}

func TestLoginLimiterReturnsMaximumRetryAfter(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 1, 8)
	limiter.allow("ip:target", "user:other")
	clock.Advance(59 * time.Second)
	limiter.allow("ip:other", "user:target")
	allowed, retry := limiter.allow("ip:target", "user:target")
	if allowed || retry != time.Minute {
		t.Fatalf("asymmetric attempt = %v, %s; want rejected for 1m", allowed, retry)
	}
}

func TestLoginLimiterCapacityChurnPreservesActiveBuckets(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 1, 2)
	limiter.allow("ip:target", "user:target")
	for i := range 100 {
		allowed, _ := limiter.allow("ip:new"+string(rune(i)), "user:new"+string(rune(i)))
		if allowed {
			t.Fatalf("capacity churn attempt %d admitted", i)
		}
	}
	if allowed, retry := limiter.allow("ip:target", "user:target"); allowed || retry != time.Minute {
		t.Fatalf("target bucket reset by churn: allowed=%v retry=%s", allowed, retry)
	}
}

func TestLoginLimiterExhaustedIPDoesNotAllocateFreshUsernames(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	const maxKeys = 4
	limiter := newLoginLimiter(clock, time.Minute, 1, maxKeys)
	if allowed, _ := limiter.allow("ip:attacker", "user:first"); !allowed {
		t.Fatal("initial attacker attempt rejected")
	}
	for index := range maxKeys + 2 {
		allowed, _ := limiter.allow("ip:attacker", "user:fresh-"+string(rune('a'+index)))
		if allowed {
			t.Fatalf("fresh username %d admitted after IP exhaustion", index)
		}
	}
	if got := limiter.tracked(); got != 2 {
		t.Fatalf("tracked buckets after rejected churn = %d, want 2", got)
	}
	if allowed, _ := limiter.allow("ip:unrelated", "user:unrelated"); !allowed {
		t.Fatal("unrelated client rejected after attacker churn")
	}
}

func TestLoginLimiterCapacityRetryReportsEarliestAvailability(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 1, 2)
	if allowed, _ := limiter.allow("ip:full", "user:full"); !allowed {
		t.Fatal("initial attempt rejected")
	}
	clock.Advance(59 * time.Second)
	allowed, retry := limiter.allow("ip:new", "user:new")
	if allowed || retry != time.Second {
		t.Fatalf("capacity attempt = %v retry %s, want rejected for 1s", allowed, retry)
	}
}

func TestLoginLimiterDoesNotEvictCurrentExistingKey(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 2, 2)
	if allowed, _ := limiter.allow("ip:existing", "username:old"); !allowed {
		t.Fatal("initial request rejected")
	}
	clock.Advance(time.Minute)
	if allowed, _ := limiter.allow("ip:existing", "username:new"); !allowed {
		t.Fatal("existing-plus-new request rejected after idle capacity became evictable")
	}
	if got := limiter.tracked(); got != 2 {
		t.Fatalf("tracked buckets = %d, want hard maximum 2", got)
	}
	if _, ok := limiter.buckets["ip:existing"]; !ok {
		t.Fatal("current existing key was evicted")
	}
}

func TestLoginLimiterEvictsOnlyIdleFullBuckets(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 1, 2)
	limiter.allow("ip:old", "user:old")
	clock.Advance(time.Minute)
	if allowed, _ := limiter.allow("ip:new", "user:new"); !allowed {
		t.Fatal("new pair rejected after old buckets became idle and full")
	}
	if got := limiter.tracked(); got != 2 {
		t.Fatalf("tracked = %d, want 2", got)
	}
}

func TestLoginLimiterBackwardClockDoesNotRefill(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	clock := testutil.NewFakeClock(start)
	limiter := newLoginLimiter(clock, time.Minute, 1, 2)
	limiter.allow("ip:one", "user:one")
	clock.Set(start.Add(-time.Hour))
	if allowed, retry := limiter.allow("ip:one", "user:one"); allowed || retry != time.Minute {
		t.Fatalf("backward-clock attempt = %v, %s; want rejected for 1m", allowed, retry)
	}
}

func TestLoginLimiterConcurrentAccess(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	limiter := newLoginLimiter(clock, time.Minute, 20, 64)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			<-start
			limiter.allow("ip:shared", "user:"+string(rune(i)))
		})
	}
	close(start)
	wg.Wait()
	if got := limiter.tracked(); got > 64 {
		t.Fatalf("tracked = %d, want <= 64", got)
	}
}
