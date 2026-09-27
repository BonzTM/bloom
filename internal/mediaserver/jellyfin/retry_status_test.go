package jellyfin

import (
	"net/http"
	"testing"
)

// A timeout reported by the upstream is a transient condition, so it must
// retry and classify as unreachable rather than malformed.
func TestRetryableStatusTreatsTimeoutsAsTransient(t *testing.T) {
	t.Parallel()
	for _, status := range []int{
		http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout,
	} {
		if !retryableStatus(status) {
			t.Fatalf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError} {
		if retryableStatus(status) {
			t.Fatalf("status %d should not be retryable", status)
		}
	}
}
