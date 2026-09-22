package testutil_test

import (
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

// FakeClock must satisfy the production core.Clock seam.
var _ core.Clock = (*testutil.FakeClock)(nil)

func TestFakeClockSetAndAdvance(t *testing.T) {
	start := time.Unix(1700000000, 0).UTC()
	c := testutil.NewFakeClock(start)

	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	c.Advance(90 * time.Second)
	if got, want := c.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Errorf("after Advance: Now() = %v, want %v", got, want)
	}

	reset := time.Unix(1800000000, 0).UTC()
	c.Set(reset)
	if got := c.Now(); !got.Equal(reset) {
		t.Errorf("after Set: Now() = %v, want %v", got, reset)
	}
}

func TestFakeClockConcurrentUse(t *testing.T) {
	c := testutil.NewFakeClock(time.Unix(0, 0).UTC())
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			c.Advance(time.Second)
			_ = c.Now()
		})
	}
	wg.Wait()
	if got, want := c.Now(), time.Unix(16, 0).UTC(); !got.Equal(want) {
		t.Errorf("Now() after 16 concurrent advances = %v, want %v", got, want)
	}
}
