package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/runtime"
	"github.com/BonzTM/bloom/internal/testutil"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// baseConfig is a valid SQLite-backed configuration rooted in a temp dir.
func baseConfig(t *testing.T, addr string) config.Config {
	t.Helper()
	return config.Config{
		HTTP: config.HTTPConfig{
			Addr: addr, ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second,
			WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxBodyBytes: 1 << 20,
		},
		Database: config.DatabaseConfig{
			Driver: config.DriverSQLite, DSN: "file:" + filepath.Join(t.TempDir(), "bloom.db") +
				"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
			MaxOpenConns: 2, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
		},
		Telemetry: config.TelemetryConfig{LogFormat: config.LogFormatJSON, TraceSampleRatio: 1},
		Auth: config.AuthConfig{
			SessionCookieSecure: true, SessionLifetime: time.Hour, SessionIdleTimeout: 15 * time.Minute,
			LoginRateRefillInterval: time.Minute, LoginRateBurst: 5, LoginRateMaxKeys: 100,
			LoginMaxConcurrent: 4,
		},
		Bootstrap: config.BootstrapConfig{Username: "admin"},
		Playback: config.PlaybackConfig{
			PollActive: 5 * time.Second, PollIdle: 30 * time.Second,
			MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
		},
		SecretKey:     config.NewSecret([]byte(testSecret)),
		ShutdownGrace: 5 * time.Second,
	}
}

func TestRunWarnsOnceWhenTrustedProxyModeIsEnabled(t *testing.T) {
	cfg := baseConfig(t, ":0")
	cfg.Migrate = true
	cfg.Auth.TrustedProxyCIDRs = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	var log strings.Builder
	if err := runtime.Run(context.Background(), cfg, runtime.Streams{Log: &log, Audit: io.Discard}); err != nil {
		t.Fatalf("Run(-migrate): %v", err)
	}
	if got := strings.Count(log.String(), "trusted proxy mode enabled"); got != 1 {
		t.Fatalf("trusted proxy warnings = %d, want 1: %s", got, log.String())
	}
	if !strings.Contains(log.String(), `"trusted_proxy_cidr_count":2`) {
		t.Errorf("warning lacks non-secret CIDR count: %s", log.String())
	}
}

func TestRunMigrateModeAppliesSchemaAndExits(t *testing.T) {
	cfg := baseConfig(t, ":0")
	cfg.Migrate = true
	cfg.Bootstrap.Password = config.NewSecret([]byte("bootstrap-secret"))
	var log strings.Builder

	if err := runtime.Run(context.Background(), cfg, runtime.Streams{Log: &log, Audit: io.Discard}); err != nil {
		t.Fatalf("Run(-migrate): %v", err)
	}
	if !strings.Contains(log.String(), "migrations applied") {
		t.Errorf("log lacks 'migrations applied': %s", log.String())
	}
	// Re-running is idempotent: goose applies nothing and still exits clean.
	if err := runtime.Run(context.Background(), cfg, runtime.Streams{Log: &log, Audit: io.Discard}); err != nil {
		t.Fatalf("second Run(-migrate): %v", err)
	}
	pool, err := db.Open(t.Context(), cfg.Database, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()
	store, _, err := db.NewAccountStores(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	if _, err := store.GetAccountByUsername(t.Context(), "admin"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("-migrate bootstrap account = %v, want ErrNotFound", err)
	}
}

func TestRunRejectsEncodedExplicitlyDisabledForeignKeys(t *testing.T) {
	cfg := baseConfig(t, ":0")
	cfg.Migrate = true
	cfg.Database.DSN = "file::memory:?%5Fpragma=FoReIgN_KeYs%280%29"

	err := runtime.Run(t.Context(), cfg, runtime.Streams{Log: io.Discard, Audit: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "verify SQLite foreign keys: disabled") {
		t.Fatalf("Run with explicitly disabled SQLite foreign keys = %v, want startup failure", err)
	}
}

func TestRunServesProbesAndStopsOnCancel(t *testing.T) {
	cfg := baseConfig(t, "127.0.0.1:0")
	cfg.Database.MigrateOnStartup = true
	var log strings.Builder

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan net.Addr, 1)
	deps := runtime.Dependencies{ListenerReady: func(addr net.Addr) { ready <- addr }}
	go func() { done <- runtime.Run(ctx, cfg, runtime.Streams{Log: &log, Audit: io.Discard}, deps) }()

	addr := listenerAddress(t, ready, done)
	client := &http.Client{}

	assertStatus(t, client, "http://"+addr+"/livez", http.StatusOK)
	assertStatus(t, client, "http://"+addr+"/readyz", http.StatusOK)
	assertStatus(t, client, "http://"+addr+"/metrics", http.StatusOK)

	resp, err := client.Get("http://" + addr + "/api/v1/version")
	if err != nil {
		t.Fatalf("GET /api/v1/version: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if body["name"] != "bloom" {
		t.Errorf("version name = %q, want bloom", body["name"])
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error on cancel: %v", err)
	}
	if !strings.Contains(log.String(), `"msg":"stopped"`) {
		t.Errorf("log lacks stopped record: %s", log.String())
	}
}

func TestRunFailsFastOnUnreachableDatabase(t *testing.T) {
	cfg := baseConfig(t, ":0")
	cfg.Database.Driver = config.DriverPostgres
	cfg.Database.DSN = "postgres://bloom:bloom@127.0.0.1:1/bloom?sslmode=disable&connect_timeout=1"

	err := runtime.Run(context.Background(), cfg, runtime.Streams{Log: io.Discard, Audit: io.Discard})
	if err == nil {
		t.Fatal("Run with unreachable postgres succeeded, want error")
	}
	if !strings.Contains(err.Error(), "open database") {
		t.Errorf("error = %v, want an open database failure", err)
	}
}

func TestRunDisabledOIDCPerformsNoDiscovery(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(provider.Close)
	cfg := baseConfig(t, "127.0.0.1:0")
	cfg.Database.MigrateOnStartup = true
	cfg.PublicURL = "https://bloom.example"
	cfg.OIDC = config.OIDCConfig{Enabled: false, IssuerURL: provider.URL}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan net.Addr, 1)
	deps := runtime.Dependencies{ListenerReady: func(addr net.Addr) { ready <- addr }}
	go func() { done <- runtime.Run(ctx, cfg, runtime.Streams{Log: io.Discard, Audit: io.Discard}, deps) }()
	_ = listenerAddress(t, ready, done)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("disabled OIDC discovery requests = %d, want 0", requests.Load())
	}
}

func TestRunEnabledOIDCFailsStartupWhenDiscoveryUnavailable(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(provider.Close)
	cfg := baseConfig(t, ":0")
	cfg.Database.MigrateOnStartup = true
	cfg.PublicURL = "https://bloom.example"
	cfg.OIDC = config.OIDCConfig{
		Enabled: true, DisplayName: "SSO", IssuerURL: provider.URL,
		ClientID: "bloom", ClientSecret: config.NewSecret([]byte("secret")),
		RedirectURL: "https://bloom.example/api/v1/auth/oidc/callback",
		Scopes:      []string{"openid"}, UsernameClaim: "preferred_username",
		AllowInsecureIssuer: true, DiscoveryTimeout: 200 * time.Millisecond,
		TokenExchangeTimeout: time.Second, JWKSFetchTimeout: time.Second,
	}
	clock := testutil.NewFakeClock(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	var waits int
	err := runtime.Run(context.Background(), cfg, runtime.Streams{Log: io.Discard, Audit: io.Discard}, runtime.Dependencies{
		Clock:      clock,
		OIDCRandom: func(time.Duration) (time.Duration, error) { return 0, nil },
		OIDCWait:   func(context.Context, time.Duration) error { waits++; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "initialize OIDC provider") {
		t.Fatalf("Run error = %v, want OIDC startup failure", err)
	}
	if requests.Load() != 3 || waits != 2 {
		t.Fatalf("discovery attempts = %d waits = %d, want 3 and 2", requests.Load(), waits)
	}
}

func TestRunFailsStartupWhenConfiguredOIDCRoleIsMissing(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.OIDCConfig)
	}{
		{name: "default role", mutate: func(cfg *config.OIDCConfig) { cfg.DefaultRole = "missing-default" }},
		{name: "role map target", mutate: func(cfg *config.OIDCConfig) {
			cfg.RoleClaim = "groups"
			cfg.RoleMap = map[string]string{"operators": "missing-mapped"}
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var discoveryRequests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				discoveryRequests.Add(1)
			}))
			t.Cleanup(provider.Close)
			cfg := baseConfig(t, "127.0.0.1:0")
			cfg.Database.MigrateOnStartup = true
			cfg.PublicURL = "https://bloom.example"
			cfg.OIDC = config.OIDCConfig{
				Enabled: true, DisplayName: "SSO", IssuerURL: provider.URL,
				ClientID: "bloom", ClientSecret: config.NewSecret([]byte("secret")),
				RedirectURL: "https://bloom.example/api/v1/auth/oidc/callback",
				Scopes:      []string{"openid"}, UsernameClaim: "preferred_username",
				AllowInsecureIssuer: true, DiscoveryTimeout: time.Second,
				TokenExchangeTimeout: time.Second, JWKSFetchTimeout: time.Second,
			}
			testCase.mutate(&cfg.OIDC)
			err := runtime.Run(context.Background(), cfg, runtime.Streams{Log: io.Discard, Audit: io.Discard})
			if err == nil || !strings.Contains(err.Error(), "configured OIDC role") {
				t.Fatalf("Run error = %v, want configured role startup failure", err)
			}
			if discoveryRequests.Load() != 0 {
				t.Fatalf("discovery requests = %d, want fail-fast before discovery", discoveryRequests.Load())
			}
		})
	}
}

func listenerAddress(t *testing.T, ready <-chan net.Addr, done <-chan error) string {
	t.Helper()
	select {
	case addr := <-ready:
		return addr.String()
	case err := <-done:
		t.Fatalf("Run exited before the listener came up: %v", err)
		return ""
	}
}

func assertStatus(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		t.Errorf("GET %s = %d, want %d", url, resp.StatusCode, want)
	}
}
