package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/runtime"
)

const sessionRestartPassword = "restart-proof-secret-2026"

type sessionAccountResponse struct {
	Account struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"account"`
}

type runtimeProcess struct {
	cancel context.CancelFunc
	done   <-chan error
	once   sync.Once
	err    error
	url    string
}

type runtimeResponse struct {
	body    []byte
	cookies []*http.Cookie
}

func TestRunSessionDurabilityAcrossRestartSQLite(t *testing.T) {
	cfg := baseConfig(t, "127.0.0.1:0")
	cfg.Database.MigrateOnStartup = true
	runSessionRestartSuite(t, cfg)
}

func runSessionRestartSuite(t *testing.T, cfg config.Config) {
	t.Helper()
	cfg.Bootstrap.Password = config.NewSecret([]byte(sessionRestartPassword))
	client := &http.Client{}

	first := startRuntime(t, cfg)
	want, active := loginRuntime(t, client, first.url)
	_, revoked := loginRuntime(t, client, first.url)
	requestRuntime(t, client, http.MethodPost, first.url+"/api/v1/auth/logout", "", revoked, http.StatusNoContent)
	first.stop(t)

	second := startRuntime(t, cfg)
	t.Run("active session survives", func(t *testing.T) {
		got := currentRuntimeAccount(t, client, second.url, active, http.StatusOK)
		if got != want {
			t.Fatalf("account after restart = %+v, want %+v", got, want)
		}
	})
	t.Run("pre-restart logout remains revoked", func(t *testing.T) {
		_ = currentRuntimeAccount(t, client, second.url, revoked, http.StatusUnauthorized)
	})
	requestRuntime(t, client, http.MethodPost, second.url+"/api/v1/auth/logout", "", active, http.StatusNoContent)
	_ = currentRuntimeAccount(t, client, second.url, active, http.StatusUnauthorized)
	second.stop(t)
}

func startRuntime(t *testing.T, cfg config.Config) *runtimeProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	ready := make(chan net.Addr, 1)
	deps := runtime.Dependencies{ListenerReady: func(addr net.Addr) { ready <- addr }}
	go func() {
		done <- runtime.Run(ctx, cfg, runtime.Streams{Log: io.Discard, Audit: io.Discard}, deps)
	}()
	select {
	case addr := <-ready:
		process := &runtimeProcess{cancel: cancel, done: done, url: "http://" + addr.String()}
		t.Cleanup(func() { process.stop(t) })
		return process
	case err := <-done:
		cancel()
		t.Fatalf("Run exited before listener readiness: %v", err)
		return nil
	}
}

func (p *runtimeProcess) stop(t *testing.T) {
	t.Helper()
	p.once.Do(func() {
		p.cancel()
		p.err = <-p.done
	})
	if p.err != nil {
		t.Fatalf("Run returned error on cancellation: %v", p.err)
	}
}

func loginRuntime(t *testing.T, client *http.Client, baseURL string) (sessionAccountResponse, *http.Cookie) {
	t.Helper()
	body := `{"username":"admin","password":"` + sessionRestartPassword + `"}`
	response := requestRuntime(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", body, nil, http.StatusOK)
	return decodeSessionAccount(t, response.body), findRuntimeSessionCookie(t, response.cookies)
}

func currentRuntimeAccount(
	t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, wantStatus int,
) sessionAccountResponse {
	t.Helper()
	response := requestRuntime(t, client, http.MethodGet, baseURL+"/api/v1/auth/me", "", cookie, wantStatus)
	if wantStatus != http.StatusOK {
		return sessionAccountResponse{}
	}
	return decodeSessionAccount(t, response.body)
}

func requestRuntime(
	t *testing.T, client *http.Client, method, url, body string, cookie *http.Cookie, wantStatus int,
) runtimeResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read %s %s response: read=%v close=%v", method, url, readErr, closeErr)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s = %d, want %d: %s", method, url, response.StatusCode, wantStatus, responseBody)
	}
	return runtimeResponse{body: responseBody, cookies: response.Cookies()}
}

func decodeSessionAccount(t *testing.T, body []byte) sessionAccountResponse {
	t.Helper()
	var account sessionAccountResponse
	if err := json.Unmarshal(body, &account); err != nil {
		t.Fatalf("decode current account: %v", err)
	}
	if account.Account.ID == "" || account.Account.Username == "" {
		t.Fatalf("current account lacks identity: %+v", account)
	}
	return account
}

func findRuntimeSessionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == "bloom_session" {
			return cookie
		}
	}
	t.Fatal("response has no bloom_session cookie")
	return nil
}
