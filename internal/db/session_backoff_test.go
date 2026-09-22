package db

import (
	"testing"
	"time"
)

func TestSessionCleanupBackoffIsExponentialAndCapped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		failures uint8
		want     time.Duration
	}{
		{failures: 0, want: time.Second},
		{failures: 1, want: time.Second},
		{failures: 2, want: 2 * time.Second},
		{failures: 3, want: 4 * time.Second},
		{failures: 9, want: 256 * time.Second},
		{failures: 10, want: 5 * time.Minute},
		{failures: 255, want: 5 * time.Minute},
	}
	for _, testCase := range tests {
		if got := sessionCleanupBackoff(testCase.failures); got != testCase.want {
			t.Errorf("sessionCleanupBackoff(%d) = %s, want %s", testCase.failures, got, testCase.want)
		}
	}
}
