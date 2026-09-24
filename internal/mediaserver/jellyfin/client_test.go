package jellyfin

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	testAPIKey   = "test-api-key"
	testDeviceID = "33333333-3333-4333-8333-333333333333"
)

type metricObservation struct{ operation, outcome string }

type recordingObserver struct {
	mu       sync.Mutex
	requests []metricObservation
	retries  []metricObservation
}

func (o *recordingObserver) ObserveMediaServerRequest(_, operation, outcome string, _ float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, metricObservation{operation: operation, outcome: outcome})
}

func (o *recordingObserver) ObserveMediaServerRetry(_, operation, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.retries = append(o.retries, metricObservation{operation: operation, outcome: outcome})
}

func TestClientHappyPathAndEncodedAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := `MediaBrowser Client="bloom", Device="bloom-server", DeviceId="` + testDeviceID + `", Version="v1.2.3%2Bbuild", Token="test%20key%22%5C%25%C3%A9"`
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = fmt.Fprint(w, `{"ServerName":"Living Room","Version":"12.1.0","Id":"server-1"}`)
		case "/Library/VirtualFolders":
			_, _ = fmt.Fprint(w, `[{"ItemId":"lib-1","Name":"Movies","CollectionType":"movies"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func(cfg *Config) {
		cfg.APIKey = "test key\"\\%é"
		cfg.Version = "v1.2.3+build"
	})
	info, err := client.Probe(context.Background())
	if err != nil || info.ID != "server-1" || info.Name != "Living Room" {
		t.Fatalf("Probe = %+v, %v", info, err)
	}
	libraries, err := client.ListLibraries(context.Background())
	if err != nil || len(libraries) != 1 || libraries[0].ID != "lib-1" {
		t.Fatalf("ListLibraries = %+v, %v", libraries, err)
	}
}

func TestClientUserProvisioningPreservesWholePolicy(t *testing.T) {
	const userID = "44444444-4444-4444-8444-444444444444"
	const libraryID = "55555555-5555-4555-8555-555555555555"
	var createCalls, getCalls, policyCalls, deleteCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/Users/New":
			createCalls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if body["Name"] != "new-user" || body["Password"] != "Th1s-is-a-unique-password!" {
				t.Errorf("create body = %#v", body)
			}
			_, _ = fmt.Fprintf(w, `{"Id":%q,"Name":"new-user"}`, userID)
		case r.Method == http.MethodGet && r.URL.Path == "/Users/"+userID:
			getCalls++
			_, _ = fmt.Fprint(w, `{"Id":"`+userID+`","Name":"new-user","Policy":{"AuthenticationProviderId":"auth","PasswordResetProviderId":"reset","IsAdministrator":true,"EnableAllFolders":true,"EnabledFolders":[]}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/Users/"+userID+"/Policy":
			policyCalls++
			var policy map[string]any
			if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
				t.Errorf("decode policy: %v", err)
			}
			if policy["IsAdministrator"] != true || policy["EnableAllFolders"] != false {
				t.Errorf("policy fields = %#v", policy)
			}
			folders, ok := policy["EnabledFolders"].([]any)
			if !ok || len(folders) != 1 || folders[0] != libraryID {
				t.Errorf("EnabledFolders = %#v", policy["EnabledFolders"])
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/Users/"+userID:
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	user, err := client.CreateUser(context.Background(), "new-user", "Th1s-is-a-unique-password!")
	if err != nil || user.ID != userID {
		t.Fatalf("CreateUser = %+v, %v", user, err)
	}
	if err := client.SetLibraryAccess(context.Background(), user.ID, []string{libraryID}, false); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	if err := client.DeleteUser(context.Background(), user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if createCalls != 1 || getCalls != 1 || policyCalls != 1 || deleteCalls != 1 {
		t.Fatalf("calls create=%d get=%d policy=%d delete=%d", createCalls, getCalls, policyCalls, deleteCalls)
	}
}

func TestClientCreateUserClassifiesFailuresWithoutRetry(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		kind      core.MediaServerErrorKind
		nameError bool
	}{
		{name: "taken or invalid name", status: http.StatusBadRequest, nameError: true},
		{name: "API key revoked", status: http.StatusUnauthorized, kind: core.MediaServerUnauthorized},
		{name: "server failure", status: http.StatusServiceUnavailable, kind: core.MediaServerUnavailable},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			postCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/Users" {
					_, _ = fmt.Fprint(w, `[]`)
					return
				}
				postCalls++
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()
			client := newTestClient(t, server, nil)
			_, err := client.CreateUser(context.Background(), "new-user", "Th1s-is-a-unique-password!")
			if testCase.nameError {
				nameErr, ok := errors.AsType[*core.MediaUserNameError](err)
				if !ok || nameErr == nil {
					t.Fatalf("error = %T %v, want MediaUserNameError", err, err)
				}
			} else {
				assertMediaError(t, err, testCase.kind)
			}
			if postCalls != 1 {
				t.Fatalf("POST /Users/New calls = %d, want 1", postCalls)
			}
		})
	}
}

func TestClientCreateUserFindsAmbiguousMalformedSuccess(t *testing.T) {
	const userID = "44444444-4444-4444-8444-444444444444"
	var listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/Users/New":
			_, _ = fmt.Fprint(w, `{}`)
		case r.Method == http.MethodGet && r.URL.Path == "/Users":
			listCalls++
			_, _ = fmt.Fprint(w, `[{"Id":"`+userID+`","Name":"new-user"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	user, err := client.CreateUser(context.Background(), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, core.ErrMediaUserCreateAmbiguous) || user.ID != userID || listCalls != 1 {
		t.Fatalf("CreateUser = %+v, %v; list calls = %d", user, err, listCalls)
	}
}

func TestClientCreateUserFindsUserAfterResponseLoss(t *testing.T) {
	const userID = "44444444-4444-4444-8444-444444444444"
	created := false
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost && request.URL.Path == "/Users/New" {
			created = true
			return nil, io.ErrUnexpectedEOF
		}
		if request.Method == http.MethodGet && request.URL.Path == "/Users" && created {
			return jsonResponse(http.StatusOK, `[{"Id":"`+userID+`","Name":"new-user"}]`), nil
		}
		return jsonResponse(http.StatusNotFound, ``), nil
	})
	client := newTransportClient(t, transport)
	user, err := client.CreateUser(context.Background(), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, core.ErrMediaUserCreateAmbiguous) || user.ID != userID {
		t.Fatalf("CreateUser = %+v, %v", user, err)
	}
}

func TestClientFindUserByNameAndID(t *testing.T) {
	const userID = "44444444-4444-4444-8444-444444444444"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/Users" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `[{"Id":"`+userID+`","Name":"alice"}]`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	byName, found, err := client.FindUserByName(context.Background(), "alice")
	if err != nil || !found || byName.ID != userID {
		t.Fatalf("FindUserByName = %+v, %t, %v", byName, found, err)
	}
	byID, found, err := client.FindUserByID(context.Background(), userID)
	if err != nil || !found || byID.Name != "alice" {
		t.Fatalf("FindUserByID = %+v, %t, %v", byID, found, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestClientPolicyAndDeleteRetryIdempotentFailures(t *testing.T) {
	const userID = "44444444-4444-4444-8444-444444444444"
	var policyCalls, deleteCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = fmt.Fprint(w, `{"Id":"`+userID+`","Name":"new-user","Policy":{"AuthenticationProviderId":"auth","PasswordResetProviderId":"reset"}}`)
		case http.MethodPost:
			policyCalls++
			if policyCalls == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			deleteCalls++
			if deleteCalls == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	if err := client.SetLibraryAccess(context.Background(), userID, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteUser(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if policyCalls != 2 || deleteCalls != 2 {
		t.Fatalf("retry calls policy=%d delete=%d", policyCalls, deleteCalls)
	}
}

func TestClientRequestBudgetCancelsStalledRequest(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	client := newTestClient(t, server, func(cfg *Config) { cfg.CallTimeout = 100 * time.Millisecond })
	_, err := client.Probe(context.Background())
	assertMediaError(t, err, core.MediaServerUnavailable)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe error = %v, want context deadline", err)
	}
	assertChannelClosed(t, started, "server request did not start")
	assertChannelClosed(t, canceled, "request cancellation did not reach server")
}

func assertChannelClosed(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func TestClientStatusClassification(t *testing.T) {
	tests := []struct {
		name          string
		status, calls int
		kind          core.MediaServerErrorKind
	}{
		{name: "unauthorized", status: 401, calls: 1, kind: core.MediaServerUnauthorized},
		{name: "forbidden", status: 403, calls: 1, kind: core.MediaServerUnauthorized},
		{name: "not found", status: 404, calls: 1, kind: core.MediaServerNotFound},
		{name: "429", status: 429, calls: 3, kind: core.MediaServerUnavailable},
		{name: "500 terminal", status: 500, calls: 1, kind: core.MediaServerMalformed},
		{name: "501 terminal", status: 501, calls: 1, kind: core.MediaServerMalformed},
		{name: "502", status: 502, calls: 3, kind: core.MediaServerUnavailable},
		{name: "503", status: 503, calls: 3, kind: core.MediaServerUnavailable},
		{name: "504", status: 504, calls: 3, kind: core.MediaServerUnavailable},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()
			client := newTestClient(t, server, nil)
			_, err := client.Probe(context.Background())
			assertMediaError(t, err, testCase.kind)
			if got := int(calls.Load()); got != testCase.calls {
				t.Fatalf("calls = %d, want %d", got, testCase.calls)
			}
		})
	}
}

func TestClientRetryAfterFormsAndBudgetCap(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name, header string
		want         time.Duration
	}{
		{name: "seconds", header: "7", want: 7 * time.Second},
		{name: "http date", header: now.Add(9 * time.Second).Format(http.TimeFormat), want: 9 * time.Second},
		{name: "capped", header: "90", want: 30 * time.Second},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", testCase.header)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			client := newTestClient(t, server, func(cfg *Config) { cfg.CallTimeout = time.Second })
			client.now = func() time.Time { return now }
			_, err := client.Probe(context.Background())
			var mediaErr *core.MediaServerError
			if !errors.As(err, &mediaErr) || mediaErr.RetryAfter != testCase.want {
				t.Fatalf("error = %#v, want RetryAfter %s", mediaErr, testCase.want)
			}
		})
	}
}

func TestParseRetryAfterCapsBeforeDurationConversion(t *testing.T) {
	tests := []string{
		strconv.FormatInt(math.MaxInt64, 10),
		strconv.FormatUint(math.MaxUint64, 10),
		strconv.FormatInt(int64(math.MaxInt64/time.Second)+1, 10),
		"18446744073709551616",
	}
	for _, value := range tests {
		got, valid := parseRetryAfter(value, time.Time{})
		if got != maxRetryAfter || !valid {
			t.Errorf("parseRetryAfter(%q) = (%s, %t), want (%s, true)", value, got, valid, maxRetryAfter)
		}
	}
}

func TestClientRetryAfterFormsArePreservedThroughSuccessfulRetry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name, header string
		want         time.Duration
	}{
		{name: "delay seconds", header: "7", want: 7 * time.Second},
		{name: "HTTP date", header: now.Add(9 * time.Second).Format(http.TimeFormat), want: 9 * time.Second},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client, sleeps := retryThenSuccessClient(t, now, testCase.header)
			if _, err := client.Probe(context.Background()); err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if len(*sleeps) != 1 || (*sleeps)[0] != testCase.want {
				t.Fatalf("retry sleeps = %v, want [%s]", *sleeps, testCase.want)
			}
		})
	}
}

func TestClientRetryAfterZeroSchedulesZeroDelay(t *testing.T) {
	client, sleeps := retryThenSuccessClient(t, time.Now().UTC(), "0")
	client.randomInt64N = func(upperExclusive int64) int64 { return upperExclusive - 1 }
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if want := []time.Duration{0}; !slices.Equal(*sleeps, want) {
		t.Fatalf("retry sleeps = %v, want %v", *sleeps, want)
	}
}

func retryThenSuccessClient(t *testing.T, now time.Time, retryAfter string) (*Client, *[]time.Duration) {
	t.Helper()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", retryAfter)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = fmt.Fprint(w, `{"ServerName":"Jellyfin","Version":"12.1.0","Id":"server-1"}`)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server, func(cfg *Config) { cfg.CallTimeout = 15 * time.Second })
	sleeps := make([]time.Duration, 0, 1)
	client.now = func() time.Time { return now }
	client.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}
	return client, &sleeps
}

func TestClientBackoffSuppliesExponentiallyBoundedJitter(t *testing.T) {
	client := retryingClient(t, 2)
	maximums := make([]time.Duration, 0, maxAttempts-1)
	sleeps := make([]time.Duration, 0, maxAttempts-1)
	client.randomInt64N = func(upperExclusive int64) int64 {
		maximums = append(maximums, time.Duration(upperExclusive-1))
		return upperExclusive - 1
	}
	client.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want := []time.Duration{backoffBase, 2 * backoffBase}
	if !slices.Equal(maximums, want) || !slices.Equal(sleeps, want) {
		t.Fatalf("jitter maximums = %v, sleeps = %v, want %v", maximums, sleeps, want)
	}
	assertBackoffSchedule(t)
}

func TestFullJitterUsesInclusiveRange(t *testing.T) {
	const maximum = 100 * time.Millisecond
	tests := []struct {
		name string
		draw int64
	}{
		{name: "zero", draw: 0},
		{name: "interior", draw: int64(37 * time.Millisecond)},
		{name: "inclusive maximum", draw: int64(maximum)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := fullJitter(maximum, func(upperExclusive int64) int64 {
				if upperExclusive != int64(maximum)+1 {
					t.Fatalf("random upper bound = %d, want %d", upperExclusive, int64(maximum)+1)
				}
				return testCase.draw
			})
			if got != time.Duration(testCase.draw) {
				t.Fatalf("fullJitter = %s, want %s", got, time.Duration(testCase.draw))
			}
		})
	}
}

func assertBackoffSchedule(t *testing.T) {
	t.Helper()
	want := []time.Duration{
		backoffBase, 2 * backoffBase, 4 * backoffBase, 8 * backoffBase,
		backoffCap, backoffCap,
	}
	for attempt, maximum := range want {
		if got := backoff(attempt); got != maximum {
			t.Errorf("backoff(%d) = %s, want %s", attempt, got, maximum)
		}
	}
}

func retryingClient(t *testing.T, failures int) *Client {
	t.Helper()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"ServerName":"Jellyfin","Version":"12.1.0","Id":"server-1"}`)
	}))
	t.Cleanup(server.Close)
	return newTestClient(t, server, nil)
}

func TestClientCancellationBetweenRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	client.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	_, err := client.Probe(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe error = %v, want context.Canceled", err)
	}
}

func TestClientNetworkRetryClassification(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		calls int
	}{
		{name: "certificate", err: x509.UnknownAuthorityError{}, calls: 1},
		{name: "timeout", err: timeoutError{}, calls: 3},
		{name: "connection refused", err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, calls: 3},
		{name: "reset", err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}, calls: 3},
		{name: "eof", err: io.EOF, calls: 3},
		{name: "terminal", err: errors.New("bad transport configuration"), calls: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			transport := &errorTransport{err: testCase.err}
			client := newTransportClient(t, transport)
			_, err := client.Probe(context.Background())
			assertMediaError(t, err, core.MediaServerUnavailable)
			var mediaErr *core.MediaServerError
			if !errors.As(err, &mediaErr) {
				t.Fatalf("Probe error = %T, want MediaServerError", err)
			}
			wantRetryable := testCase.calls > 1
			if mediaErr.Retryable != wantRetryable {
				t.Errorf("Retryable = %t, want %t", mediaErr.Retryable, wantRetryable)
			}
			if transport.calls != testCase.calls {
				t.Fatalf("calls = %d, want %d", transport.calls, testCase.calls)
			}
		})
	}
}

func TestClientMalformedAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{name: "invalid JSON", body: `{`},
		{name: "oversized", body: strings.Repeat("x", maxResponseBytes+1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, testCase.body)
			}))
			defer server.Close()
			client := newTestClient(t, server, nil)
			_, err := client.Probe(context.Background())
			assertMediaError(t, err, core.MediaServerMalformed)
		})
	}
}

func TestClientRejectsTooManyLibraries(t *testing.T) {
	body := "[" + strings.Repeat(`{"ItemId":"id","Name":"name"},`, core.MaxMediaServerLibraries) +
		`{"ItemId":"id","Name":"name"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListLibraries(context.Background())
	assertMediaError(t, err, core.MediaServerMalformed)
}

func TestClientRetryMetrics(t *testing.T) {
	tests := []struct {
		name          string
		succeedAfter  int
		wantScheduled int
		wantExhausted int
	}{
		{name: "scheduled", succeedAfter: 1, wantScheduled: 1},
		{name: "exhausted", succeedAfter: maxAttempts, wantScheduled: 2, wantExhausted: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls <= testCase.succeedAfter {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = fmt.Fprint(w, `{"ServerName":"Jellyfin","Version":"12.1.0","Id":"server-1"}`)
			}))
			defer server.Close()
			observer := &recordingObserver{}
			client := newTestClient(t, server, func(cfg *Config) { cfg.Observer = observer })
			_, err := client.Probe(context.Background())
			if (testCase.wantExhausted == 0) != (err == nil) {
				t.Fatalf("Probe error = %v", err)
			}
			assertRetryOutcomes(t, observer.retries, testCase.wantScheduled, testCase.wantExhausted, 0)
		})
	}
}

func TestClientRetryMetricsBudgetExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	observer := &recordingObserver{}
	client := newTestClient(t, server, func(cfg *Config) {
		cfg.CallTimeout = time.Second
		cfg.Observer = observer
	})
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("Probe succeeded, want budget exhaustion")
	}
	assertRetryOutcomes(t, observer.retries, 0, 0, 1)
}

func assertRetryOutcomes(t *testing.T, observations []metricObservation, scheduled, exhausted, budgetExhausted int) {
	t.Helper()
	counts := make(map[string]int)
	for _, observation := range observations {
		counts[observation.outcome]++
	}
	if counts["scheduled"] != scheduled || counts["exhausted"] != exhausted ||
		counts["budget_exhausted"] != budgetExhausted {
		t.Fatalf("retry observations = %+v", observations)
	}
}

func TestClientBodyReadFailureRetries(t *testing.T) {
	transport := &bodyErrorTransport{}
	client := newTransportClient(t, transport)
	_, err := client.Probe(context.Background())
	assertMediaError(t, err, core.MediaServerUnavailable)
	if transport.calls != maxAttempts {
		t.Fatalf("calls = %d, want %d", transport.calls, maxAttempts)
	}
}

func TestClientMissingRequiredFieldsAreMalformedAndNotSuccessful(t *testing.T) {
	tests := []struct{ name, path, body string }{
		{name: "system info", path: "/System/Info", body: `{"Version":"12.1.0","Id":"id"}`},
		{name: "library", path: "/Library/VirtualFolders", body: `[{"Name":"Movies"}]`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != testCase.path {
					http.NotFound(w, r)
					return
				}
				_, _ = fmt.Fprint(w, testCase.body)
			}))
			defer server.Close()
			observer := &recordingObserver{}
			client := newTestClient(t, server, func(cfg *Config) { cfg.Observer = observer })
			var err error
			if testCase.path == "/System/Info" {
				_, err = client.Probe(context.Background())
			} else {
				_, err = client.ListLibraries(context.Background())
			}
			assertMediaError(t, err, core.MediaServerMalformed)
			if len(observer.requests) != 1 || observer.requests[0].outcome != "malformed" {
				t.Fatalf("observations = %+v", observer.requests)
			}
		})
	}
}

func TestClientEmitsOutboundSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"ServerName":"Jellyfin","Version":"12.1.0","Id":"server-1"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(recorder.Ended()) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(recorder.Ended()))
	}
}

func TestSafeDialRejectsEveryDeniedAddressClass(t *testing.T) {
	tests := map[string]netip.Addr{
		"invalid": {}, "unspecified": netip.MustParseAddr("0.0.0.0"),
		"loopback": netip.MustParseAddr("127.0.0.1"), "multicast": netip.MustParseAddr("224.0.0.1"),
		"link local unicast":       netip.MustParseAddr("169.254.1.1"),
		"link local multicast":     netip.MustParseAddr("ff02::1"),
		"IPv4 metadata":            netip.MustParseAddr("169.254.169.254"),
		"IPv6 metadata compressed": netip.MustParseAddr("fd00:ec2::254"),
		"IPv6 metadata expanded": netip.MustParseAddr(
			"fd00:0ec2:0000:0000:0000:0000:0000:0254",
		),
		"IPv6 metadata scoped compressed": netip.MustParseAddr("fd00:ec2::254%eth0"),
		"IPv6 metadata scoped expanded": netip.MustParseAddr(
			"fd00:0ec2:0000:0000:0000:0000:0000:0254%eth0",
		),
		"IPv6 scoped private": netip.MustParseAddr("fd12::1%eth0"),
		"IPv4-mapped scoped":  netip.MustParseAddr("::ffff:192.168.1.10%eth0"),
	}
	for name, address := range tests {
		t.Run(name, func(t *testing.T) {
			dialer := &recordingDialer{}
			dial := safeDialContext(staticResolver{addresses: []netip.Addr{address}}, dialer)
			if connection, err := dial(context.Background(), "tcp", "media.test:443"); err == nil {
				_ = connection.Close()
				t.Fatal("safe dial accepted denied destination")
			}
			if len(dialer.addresses) != 0 {
				t.Fatalf("dialed denied addresses %v", dialer.addresses)
			}
		})
	}
}

func TestSafeDialRejectsMixedAndExcessiveResolution(t *testing.T) {
	tests := []struct {
		name      string
		addresses []netip.Addr
	}{
		{name: "mixed", addresses: []netip.Addr{
			netip.MustParseAddr("192.168.1.10"), netip.MustParseAddr("127.0.0.1"),
		}},
		{name: "too many", addresses: repeatedAddress(maxResolvedAddresses + 1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			dialer := &recordingDialer{}
			dial := safeDialContext(staticResolver{addresses: testCase.addresses}, dialer)
			if _, err := dial(context.Background(), "tcp", "media.test:443"); err == nil {
				t.Fatal("safe dial accepted denied resolution")
			}
			if len(dialer.addresses) != 0 {
				t.Fatalf("dial attempts = %v, want none", dialer.addresses)
			}
		})
	}
}

func TestSafeDialRevalidatesResolutionForEveryRequest(t *testing.T) {
	resolver := &sequenceResolver{responses: [][]netip.Addr{
		{netip.MustParseAddr("192.168.1.10")},
		{netip.MustParseAddr("127.0.0.1")},
	}}
	dialer := &recordingDialer{}
	dial := safeDialContext(resolver, dialer)
	connection, err := dial(context.Background(), "tcp", "media.test:443")
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	_ = connection.Close()
	if _, err := dial(context.Background(), "tcp", "media.test:443"); err == nil {
		t.Fatal("second dial accepted changed loopback resolution")
	}
	if resolver.calls != 2 || len(dialer.addresses) != 1 {
		t.Fatalf("resolver calls = %d, dial attempts = %v", resolver.calls, dialer.addresses)
	}
}

func TestSafeTransportIgnoresProxyEnvironmentAtDialBoundary(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9999")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9999")
	dialer := &recordingDialer{}
	transport := newSafeTransportWith(
		staticResolver{addresses: []netip.Addr{netip.MustParseAddr("192.168.1.10")}}, dialer,
	)
	if transport.Proxy != nil {
		t.Fatal("safe transport configured a proxy")
	}
	connection, err := transport.DialContext(context.Background(), "tcp", "media.test:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = connection.Close()
	if !slices.Equal(dialer.addresses, []string{"192.168.1.10:443"}) {
		t.Fatalf("dial attempts = %v", dialer.addresses)
	}
}

func TestClientRejectsInsecureURLWithoutOverride(t *testing.T) {
	_, err := New(Config{BaseURL: "http://media.example.test", APIKey: testAPIKey, DeviceID: testDeviceID})
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("New error = %v, want ErrInvalidArgument", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server, mutate func(*Config)) *Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	cfg := Config{
		BaseURL: "https://media.example.test", APIKey: testAPIKey, Version: "v1.2.3",
		DeviceID: testDeviceID, CallTimeout: time.Second,
		HTTPClient: &http.Client{Transport: rewriteTransport{target: target, base: http.DefaultTransport}},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	client.randomInt64N = func(int64) int64 { return 0 }
	return client
}

func newTransportClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL: "https://media.example.test", APIKey: testAPIKey, Version: "dev",
		DeviceID: testDeviceID, CallTimeout: time.Second, HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	client.randomInt64N = func(int64) int64 { return 0 }
	return client
}

func assertMediaError(t *testing.T, err error, kind core.MediaServerErrorKind) {
	t.Helper()
	var mediaErr *core.MediaServerError
	if !errors.As(err, &mediaErr) || mediaErr.Kind != kind {
		t.Fatalf("error = %T %v, want kind %q", err, err, kind)
	}
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme, clone.URL.Host = t.target.Scheme, t.target.Host
	return t.base.RoundTrip(clone)
}

type errorTransport struct {
	err   error
	calls int
}

func (t *errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, t.err
}

type bodyErrorTransport struct{ calls int }

func (t *bodyErrorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
		Body: io.NopCloser(&failingReader{}),
	}, nil
}

type failingReader struct{ sent bool }

func (r *failingReader) Read(buffer []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(buffer, strings.Repeat("x", 8)), nil
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type staticResolver struct{ addresses []netip.Addr }

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.addresses, nil
}

type sequenceResolver struct {
	responses [][]netip.Addr
	calls     int
}

func (r *sequenceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	response := r.responses[r.calls]
	r.calls++
	return response, nil
}

type recordingDialer struct{ addresses []string }

func (d *recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func repeatedAddress(count int) []netip.Addr {
	addresses := make([]netip.Addr, count)
	for index := range count {
		addresses[index] = netip.MustParseAddr("192.168.1.10")
	}
	return addresses
}
