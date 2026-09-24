package db

import (
	"errors"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestEnforceStatsBucketRowLimit(t *testing.T) {
	t.Parallel()
	if err := enforceStatsBucketRowLimit(core.MaxStatsBucketRows); err != nil {
		t.Fatalf("maximum row count rejected: %v", err)
	}
	if err := enforceStatsBucketRowLimit(core.MaxStatsBucketRows + 1); !errors.Is(err, core.ErrStatsRowLimit) {
		t.Fatalf("overflow error = %v, want ErrStatsRowLimit", err)
	}
}
