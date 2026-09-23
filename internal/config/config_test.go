package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// testSecret is a 32-byte key that satisfies the minimum length.
const testSecret = "0123456789abcdef0123456789abcdef"

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("BLOOM_SECRET_KEY", testSecret)
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_HTTP_ADDR", ":9090")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.HTTP.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090 (env precedence)", cfg.HTTP.Addr)
	}
	if cfg.HTTP.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want default %d", cfg.HTTP.MaxBodyBytes, defaultMaxBodyBytes)
	}
	if cfg.Database.Driver != DriverSQLite {
		t.Errorf("Driver = %q, want sqlite", cfg.Database.Driver)
	}
	if cfg.Database.DSN != DefaultSQLiteDSN {
		t.Errorf("DSN = %q, want default sqlite DSN", cfg.Database.DSN)
	}
	if !strings.Contains(cfg.Database.DSN, "_pragma=foreign_keys(1)") {
		t.Errorf("DSN = %q, want foreign keys enabled on every connection", cfg.Database.DSN)
	}
	if cfg.Telemetry.LogFormat != LogFormatJSON {
		t.Errorf("LogFormat = %q, want json", cfg.Telemetry.LogFormat)
	}
	if !cfg.Auth.SessionCookieSecure {
		t.Error("SessionCookieSecure = false, want secure-by-default")
	}
	if cfg.Auth.SessionLifetime != defaultSessionLifetime {
		t.Errorf("SessionLifetime = %s, want %s", cfg.Auth.SessionLifetime, defaultSessionLifetime)
	}
	if cfg.Auth.SessionIdleTimeout != defaultSessionIdleTimeout {
		t.Errorf("SessionIdleTimeout = %s, want %s", cfg.Auth.SessionIdleTimeout, defaultSessionIdleTimeout)
	}
	if cfg.Auth.LoginRateBurst != defaultLoginRateBurst || cfg.Auth.LoginRateMaxKeys != defaultLoginRateMaxKeys {
		t.Errorf("login rate defaults = burst %d max %d", cfg.Auth.LoginRateBurst, cfg.Auth.LoginRateMaxKeys)
	}
	if cfg.Auth.LoginMaxConcurrent != defaultLoginMaxConcurrent {
		t.Errorf("LoginMaxConcurrent = %d, want %d", cfg.Auth.LoginMaxConcurrent, defaultLoginMaxConcurrent)
	}
	if len(cfg.Auth.TrustedProxyCIDRs) != 0 {
		t.Errorf("TrustedProxyCIDRs default = %v, want disabled", cfg.Auth.TrustedProxyCIDRs)
	}
	if cfg.Bootstrap.Username != "admin" || cfg.Bootstrap.Password.Len() != 0 {
		t.Errorf("Bootstrap defaults = username %q password length %d", cfg.Bootstrap.Username, cfg.Bootstrap.Password.Len())
	}
	if cfg.ShutdownGrace != defaultShutdownGrace {
		t.Errorf("ShutdownGrace = %s, want %s", cfg.ShutdownGrace, defaultShutdownGrace)
	}
}

func TestLoadBootstrapConfigFromEnvironment(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_BOOTSTRAP_USERNAME", "ＯWNER")
	t.Setenv("BLOOM_BOOTSTRAP_PASSWORD", "bootstrap-secret")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Bootstrap.Username != "owner" {
		t.Errorf("Bootstrap.Username = %q, want owner", cfg.Bootstrap.Username)
	}
	if got := string(cfg.Bootstrap.Password.Bytes()); got != "bootstrap-secret" {
		t.Fatal("Bootstrap.Password did not round-trip")
	}
	if rendered := fmt.Sprintf("%+v", cfg); strings.Contains(rendered, "bootstrap-secret") {
		t.Fatalf("config rendering leaked bootstrap password: %s", rendered)
	}
}

func TestLoadRejectsInvalidBootstrapUsername(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_BOOTSTRAP_USERNAME", "-admin")

	_, err := Load(nil)
	if err == nil || !strings.Contains(err.Error(), "BLOOM_BOOTSTRAP_USERNAME") {
		t.Fatalf("Load invalid bootstrap username = %v", err)
	}
}

func TestLoadSecretKeyRequired(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"missing", ""},
		{"too short", "short"},
		{"31 bytes", strings.Repeat("x", MinSecretKeyBytes-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BLOOM_SECRET_KEY", tt.key)
			_, err := Load(nil)
			if err == nil {
				t.Fatal("Load without a valid BLOOM_SECRET_KEY: expected error, got nil")
			}
			if !strings.Contains(err.Error(), "BLOOM_SECRET_KEY") {
				t.Errorf("error %q does not name BLOOM_SECRET_KEY", err)
			}
			if strings.Contains(err.Error(), tt.key) && tt.key != "" {
				t.Errorf("error %q leaks the secret value", err)
			}
		})
	}
}

func TestSecretNeverRenders(t *testing.T) {
	setRequired(t)
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.SecretKey.Len(); got != len(testSecret) {
		t.Fatalf("Len = %d, want %d", got, len(testSecret))
	}
	if string(cfg.SecretKey.Bytes()) != testSecret {
		t.Fatal("Bytes() did not round-trip the secret")
	}

	renderings := map[string]string{
		"%v":     fmt.Sprintf("%v", cfg),
		"%+v":    fmt.Sprintf("%+v", cfg),
		"%#v":    fmt.Sprintf("%#v", cfg),
		"String": cfg.SecretKey.String(),
		"error":  fmt.Errorf("wrap: %v", cfg.SecretKey).Error(),
	}
	var sb strings.Builder
	slog.New(slog.NewJSONHandler(&sb, nil)).Info("cfg", "secret", cfg.SecretKey)
	renderings["slog"] = sb.String()
	js, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	renderings["json"] = string(js)

	for name, out := range renderings {
		if strings.Contains(out, testSecret) {
			t.Errorf("%s rendering leaks the secret: %s", name, out)
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("%s rendering does not show the redaction marker: %s", name, out)
		}
	}
}

func TestLoadPostgresRequiresDSN(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_DB_DRIVER", "postgres")

	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "BLOOM_DB_DSN") {
		t.Fatalf("Load(postgres, no DSN) = %v, want BLOOM_DB_DSN error", err)
	}

	cfg, err := Load([]string{"-db-dsn", "postgres://localhost:5432/bloom"})
	if err != nil {
		t.Fatalf("Load(postgres with DSN): %v", err)
	}
	if cfg.Database.Driver != DriverPostgres {
		t.Errorf("Driver = %q, want postgres", cfg.Database.Driver)
	}
}

func TestLoadRejectsUnknownDriver(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_DB_DRIVER", "mysql")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "BLOOM_DB_DRIVER") {
		t.Fatalf("Load(mysql) = %v, want BLOOM_DB_DRIVER error", err)
	}
}

func TestLoadMigrateMode(t *testing.T) {
	setRequired(t)

	cfg, err := Load([]string{"-migrate"})
	if err != nil {
		t.Fatalf("Load(-migrate): %v", err)
	}
	if !cfg.Migrate {
		t.Error("Migrate = false, want true")
	}
	if cfg.Database.MigrateOnStartup {
		t.Error("MigrateOnStartup default = true, want false")
	}
}

func TestLoadFlagsBeatEnv(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_HTTP_ADDR", ":1111")
	t.Setenv("BLOOM_LOG_LEVEL", "warn")
	t.Setenv("BLOOM_LOG_FORMAT", "text")

	cfg, err := Load([]string{"-http-addr", ":2222"})
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.HTTP.Addr != ":2222" {
		t.Errorf("Addr = %q, want :2222 (flag beats env)", cfg.HTTP.Addr)
	}
	if cfg.Telemetry.LogLevel != slog.LevelWarn {
		t.Errorf("LogLevel = %v, want warn (from env)", cfg.Telemetry.LogLevel)
	}
	if cfg.Telemetry.LogFormat != LogFormatText {
		t.Errorf("LogFormat = %q, want text (from env)", cfg.Telemetry.LogFormat)
	}
}

func TestLoadAuthConfigFromEnvironment(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_SESSION_COOKIE_SECURE", "false")
	t.Setenv("BLOOM_SESSION_LIFETIME", "12h")
	t.Setenv("BLOOM_SESSION_IDLE_TIMEOUT", "15m")
	t.Setenv("BLOOM_LOGIN_RATE_REFILL_INTERVAL", "30s")
	t.Setenv("BLOOM_LOGIN_RATE_BURST", "7")
	t.Setenv("BLOOM_LOGIN_RATE_MAX_KEYS", "500")
	t.Setenv("BLOOM_LOGIN_MAX_CONCURRENT", "7")
	t.Setenv("BLOOM_TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 2001:db8::/32")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.SessionCookieSecure || cfg.Auth.SessionLifetime != 12*time.Hour || cfg.Auth.SessionIdleTimeout != 15*time.Minute {
		t.Errorf("session config = %+v", cfg.Auth)
	}
	if cfg.Auth.LoginRateRefillInterval != 30*time.Second || cfg.Auth.LoginRateBurst != 7 || cfg.Auth.LoginRateMaxKeys != 500 {
		t.Errorf("rate config = %+v", cfg.Auth)
	}
	if cfg.Auth.LoginMaxConcurrent != 7 {
		t.Errorf("LoginMaxConcurrent = %d, want 7", cfg.Auth.LoginMaxConcurrent)
	}
	if len(cfg.Auth.TrustedProxyCIDRs) != 2 {
		t.Errorf("TrustedProxyCIDRs = %v, want two prefixes", cfg.Auth.TrustedProxyCIDRs)
	}
}

func TestLoadRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,not-a-cidr")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "BLOOM_TRUSTED_PROXY_CIDRS") {
		t.Fatalf("Load invalid trusted proxy = %v", err)
	}
}

func TestLoadInvalidEnums(t *testing.T) {
	tests := []struct {
		key, bad string
	}{
		{"BLOOM_LOG_LEVEL", "loud"},
		{"BLOOM_LOG_FORMAT", "xml"},
		{"BLOOM_TRACE_SAMPLE_RATIO", "1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tt.key, tt.bad)
			_, err := Load(nil)
			if err == nil {
				t.Fatalf("Load with %s=%q: expected error, got nil", tt.key, tt.bad)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not name the offending key %q", err, tt.key)
			}
		})
	}
}

func TestLoadMalformedEnvRejected(t *testing.T) {
	// A malformed env value must be rejected with an actionable, key-named
	// error, never silently defaulted, per the handbook's fail-fast contract.
	tests := []struct {
		name string
		key  string
		bad  string
	}{
		{"malformed int", "BLOOM_DB_MAX_OPEN_CONNS", "abc"},
		{"malformed auth int", "BLOOM_LOGIN_MAX_CONCURRENT", "many"},
		{"malformed int64", "BLOOM_HTTP_MAX_BODY_BYTES", "not-a-number"},
		{"malformed duration", "BLOOM_HTTP_READ_TIMEOUT", "15"}, // no unit
		{"malformed bool", "BLOOM_DB_MIGRATE_ON_STARTUP", "maybe"},
		{"malformed auth bool", "BLOOM_SESSION_COOKIE_SECURE", "maybe"},
		{"malformed float", "BLOOM_TRACE_SAMPLE_RATIO", "half"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tt.key, tt.bad)
			_, err := Load(nil)
			if err == nil {
				t.Fatalf("Load with %s=%q: expected error, got nil (silent default?)", tt.key, tt.bad)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not name the offending key %q", err, tt.key)
			}
		})
	}
}

func validConfigForTest() Config {
	return Config{
		HTTP:      HTTPConfig{Addr: ":0", ReadHeaderTimeout: time.Second, WriteTimeout: time.Second, MaxBodyBytes: 1},
		Database:  DatabaseConfig{Driver: DriverSQLite, DSN: "file::memory:?_pragma=foreign_keys(1)", MaxOpenConns: 5, MaxIdleConns: 5, ConnMaxLifetime: time.Minute},
		Telemetry: TelemetryConfig{LogFormat: LogFormatJSON, TraceSampleRatio: 1},
		Auth: AuthConfig{
			SessionCookieSecure: true, SessionLifetime: time.Hour, SessionIdleTimeout: time.Minute,
			LoginRateRefillInterval: time.Minute, LoginRateBurst: 5, LoginRateMaxKeys: 100,
			LoginMaxConcurrent: 4,
		},
		Bootstrap: BootstrapConfig{Username: defaultBootstrapUsername},
		SecretKey: NewSecret([]byte(testSecret)), ShutdownGrace: time.Second,
	}
}

func TestValidatePoolInvariants(t *testing.T) {
	if err := validConfigForTest().Validate(); err != nil {
		t.Fatalf("baseline Validate: %v", err)
	}

	bad := validConfigForTest()
	bad.Database.MaxIdleConns = 10
	if err := bad.Validate(); err == nil {
		t.Error("MaxIdleConns > MaxOpenConns accepted, want error")
	}
	bad = validConfigForTest()
	bad.Database.MaxOpenConns = 0
	if err := bad.Validate(); err == nil {
		t.Error("MaxOpenConns = 0 accepted, want error")
	}
	bad = validConfigForTest()
	bad.Database.ConnMaxLifetime = 0
	if err := bad.Validate(); err == nil {
		t.Error("ConnMaxLifetime = 0 accepted, want error")
	}
	bad = validConfigForTest()
	bad.ShutdownGrace = 0
	if err := bad.Validate(); err == nil {
		t.Error("ShutdownGrace = 0 accepted, want error")
	}
}

func TestValidateRequiresWriteTimeoutForBoundedAuthWork(t *testing.T) {
	bad := validConfigForTest()
	bad.HTTP.WriteTimeout = 0
	if err := bad.Validate(); err == nil {
		t.Error("WriteTimeout = 0 accepted, want error")
	}
}

func TestValidateAuthInvariants(t *testing.T) {
	bad := validConfigForTest()
	bad.Auth.SessionLifetime = 0
	if err := bad.Validate(); err == nil {
		t.Error("SessionLifetime = 0 accepted, want error")
	}
	bad = validConfigForTest()
	bad.Auth.SessionIdleTimeout = 2 * time.Hour
	if err := bad.Validate(); err == nil {
		t.Error("SessionIdleTimeout > SessionLifetime accepted, want error")
	}
	bad = validConfigForTest()
	bad.Auth.LoginRateMaxKeys = 1
	if err := bad.Validate(); err == nil {
		t.Error("LoginRateMaxKeys = 1 accepted, want error")
	}
	bad = validConfigForTest()
	bad.Auth.LoginRateMaxKeys = maxLoginRateMaxKeys + 1
	if err := bad.Validate(); err == nil {
		t.Error("oversized LoginRateMaxKeys accepted, want error")
	}
	bad = validConfigForTest()
	bad.Auth.LoginMaxConcurrent = 0
	if err := bad.Validate(); err == nil {
		t.Error("LoginMaxConcurrent = 0 accepted, want error")
	}
	bad = validConfigForTest()
	bad.Auth.LoginMaxConcurrent = maxLoginMaxConcurrent + 1
	if err := bad.Validate(); err == nil {
		t.Error("oversized LoginMaxConcurrent accepted, want error")
	}
}
