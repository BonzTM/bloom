package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const immutableAssetPolicy = "public, max-age=31536000, immutable"

func TestSessionMiddlewareDoesNotWrapSPAAssets(t *testing.T) {
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", immutableAssetPolicy)
		if _, err := w.Write([]byte("asset")); err != nil {
			t.Errorf("write asset: %v", err)
		}
	})
	h := newAuthHarnessWithWeb(t, web)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	rec := h.request(t, http.MethodGet, "/assets/app.2f13c.js", "", cookie)

	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != immutableAssetPolicy {
		t.Fatalf("asset response = %d Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("asset response Set-Cookie = %v, want none", got)
	}
}

func TestSessionAwareAPIResponsesDisableCachingAndVaryOnCookie(t *testing.T) {
	h := newAuthHarness(t, nil)
	login := h.login(t, "alice", "secret-password")
	assertSessionCacheHeaders(t, login.Header())
	cookie := sessionCookie(t, login)
	assertSessionCacheHeaders(t, h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie).Header())
}

func assertSessionCacheHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", header.Get("Cache-Control"))
	}
	if !headerHasToken(header.Values("Vary"), "Cookie") {
		t.Errorf("Vary = %v, want Cookie", header.Values("Vary"))
	}
}

func headerHasToken(values []string, want string) bool {
	for _, value := range values {
		for token := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}

type blockingIdentity struct {
	started  chan struct{}
	release  chan struct{}
	inFlight atomic.Int32
	maximum  atomic.Int32
}

func (i *blockingIdentity) Authenticate(ctx context.Context, _, _ string) (core.Account, error) {
	current := i.inFlight.Add(1)
	defer i.inFlight.Add(-1)
	for maximum := i.maximum.Load(); current > maximum; maximum = i.maximum.Load() {
		if i.maximum.CompareAndSwap(maximum, current) {
			break
		}
	}
	i.started <- struct{}{}
	select {
	case <-i.release:
		return core.Account{ID: "acct-alice", Username: "alice"}, nil
	case <-ctx.Done():
		return core.Account{}, ctx.Err()
	}
}

func TestLoginPasswordVerificationConcurrencyIsBounded(t *testing.T) {
	identity := &blockingIdentity{}
	h := newAuthHarnessConfigured(t, func(cfg *config.AuthConfig) {
		cfg.LoginMaxConcurrent = 2
	}, nil, nil, identity)
	synctest.Test(t, func(t *testing.T) {
		identity.started = make(chan struct{}, 3)
		identity.release = make(chan struct{})
		defer releaseBlockingIdentity(identity)
		responses := startTwoBlockedLogins(t, h, identity)

		overloads := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			overloads <- h.request(t, http.MethodPost, "/api/v1/auth/login",
				`{"username":"excess","password":"password"}`, nil)
		}()
		synctest.Wait()
		var overload *httptest.ResponseRecorder
		select {
		case overload = <-overloads:
		default:
			t.Fatal("excess login queued instead of failing fast")
		}
		assertOverloadResponse(t, overload)
		releaseBlockingIdentity(identity)
		for range 2 {
			if status := <-responses; status != http.StatusOK {
				t.Errorf("admitted login = %d, want 200", status)
			}
		}
		if got := identity.maximum.Load(); got != 2 {
			t.Errorf("maximum concurrent verifications = %d, want 2", got)
		}
	})
}

func TestLoginOverloadDoesNotDebitRateLimit(t *testing.T) {
	h := newAuthHarness(t, func(cfg *config.AuthConfig) {
		cfg.LoginRateBurst = 2
		cfg.LoginMaxConcurrent = 1
	})
	h.server.passwordVerifications <- struct{}{}
	for range 3 {
		assertOverloadResponse(t, h.login(t, "alice", "wrong"))
	}
	if event := h.audit.last(t); event.Actor != "anonymous" || event.SubjectID == "" || event.Resource != auditResourceAuthLogin || strings.Contains(event.SubjectID, "alice") {
		t.Fatalf("overload audit identity = %+v", event)
	}
	<-h.server.passwordVerifications
	for range 2 {
		if rec := h.login(t, "alice", "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("post-overload burst response = %d, want 401", rec.Code)
		}
	}
	if rec := h.login(t, "alice", "wrong"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("post-overload exhausted response = %d, want 429", rec.Code)
	}
}

func assertOverloadResponse(t *testing.T, overload *httptest.ResponseRecorder) {
	t.Helper()
	if overload.Code != http.StatusServiceUnavailable {
		t.Fatalf("excess login = %d %s, want 503", overload.Code, overload.Body.String())
	}
	if overload.Header().Get("Retry-After") == "" || decodeEnvelope(t, overload).Code != codeUnavailable {
		t.Fatalf("overload headers/body = %v %s", overload.Header(), overload.Body.String())
	}
}

func releaseBlockingIdentity(identity *blockingIdentity) {
	select {
	case <-identity.release:
	default:
		close(identity.release)
	}
}

func startTwoBlockedLogins(t *testing.T, h authHarness, identity *blockingIdentity) <-chan int {
	t.Helper()
	responses := make(chan int, 2)
	for index := range 2 {
		go func(username string) {
			rec := h.request(t, http.MethodPost, "/api/v1/auth/login",
				`{"username":"`+username+`","password":"password"}`, nil)
			responses <- rec.Code
		}("user-" + string(rune('a'+index)))
	}
	for range 2 {
		<-identity.started
	}
	return responses
}

type deadlineIdentity struct {
	remaining chan time.Duration
}

func (i *deadlineIdentity) Authenticate(ctx context.Context, _, _ string) (core.Account, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		i.remaining <- 0
		return core.Account{}, context.Canceled
	}
	i.remaining <- time.Until(deadline)
	<-ctx.Done()
	return core.Account{}, ctx.Err()
}

func TestIdentityAndLiveAccountCallsUseBoundedContexts(t *testing.T) {
	identity := &deadlineIdentity{}
	identityHarness := newAuthHarnessConfigured(t, nil, nil, nil, identity)
	accountHarness := newAuthHarness(t, nil)
	cookie := sessionCookie(t, accountHarness.login(t, "alice", "secret-password"))

	synctest.Test(t, func(t *testing.T) {
		identityDeadlines := make(chan time.Duration, 1)
		identity.remaining = identityDeadlines
		rec := identityHarness.login(t, "alice", "secret-password")
		assertBoundedDependencyCall(t, rec, <-identityDeadlines, identityHarness.server.authOperationTimeout)
	})

	synctest.Test(t, func(t *testing.T) {
		accountDeadlines := make(chan time.Duration, 1)
		accountHarness.store.blockLoads(accountDeadlines)
		rec := accountHarness.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
		assertBoundedDependencyCall(t, rec, <-accountDeadlines, accountHarness.server.authOperationTimeout)
	})
}

func assertBoundedDependencyCall(
	t *testing.T,
	rec interface{ Result() *http.Response },
	remaining time.Duration,
	want time.Duration,
) {
	t.Helper()
	if rec.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("blocked dependency status = %d, want 500", rec.Result().StatusCode)
	}
	if remaining != want {
		t.Errorf("dependency deadline remaining = %s, want %s", remaining, want)
	}
}
