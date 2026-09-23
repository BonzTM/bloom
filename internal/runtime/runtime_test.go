package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/runtime"
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
			Driver: config.DriverSQLite, DSN: "file:" + filepath.Join(t.TempDir(), "bloom.db") + "?_pragma=foreign_keys(1)",
			MaxOpenConns: 2, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
		},
		Telemetry: config.TelemetryConfig{LogFormat: config.LogFormatJSON, TraceSampleRatio: 1},
		Auth: config.AuthConfig{
			SessionCookieSecure: true, SessionLifetime: time.Hour, SessionIdleTimeout: 15 * time.Minute,
			LoginRateRefillInterval: time.Minute, LoginRateBurst: 5, LoginRateMaxKeys: 100,
			LoginMaxConcurrent: 4,
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

// freeAddr reserves and releases a loopback port so the server under test can
// bind a known address.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestRunMigrateModeAppliesSchemaAndExits(t *testing.T) {
	cfg := baseConfig(t, ":0")
	cfg.Migrate = true
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
}

func TestRunServesProbesAndStopsOnCancel(t *testing.T) {
	addr := freeAddr(t)
	cfg := baseConfig(t, addr)
	cfg.Database.MigrateOnStartup = true
	var log strings.Builder

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx, cfg, runtime.Streams{Log: &log, Audit: io.Discard}) }()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForListener(t, client, "http://"+addr+"/livez", done)

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
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop within 10s of cancellation")
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

// waitForListener polls url until it answers, failing fast if Run exits first
// (its error is far more useful than a listener timeout).
func waitForListener(t *testing.T, client *http.Client, url string, done <-chan error) {
	t.Helper()
	const attempts = 250 // 250 * 20ms = 5s upper bound
	for range attempts {
		select {
		case err := <-done:
			t.Fatalf("Run exited before the listener came up: %v", err)
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		var netErr net.Error
		if !errors.As(err, &netErr) && !strings.Contains(err.Error(), "connection refused") {
			t.Fatalf("unexpected error waiting for listener: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("listener did not come up within 5s")
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
