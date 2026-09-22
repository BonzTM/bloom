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
	if cfg.Telemetry.LogFormat != LogFormatJSON {
		t.Errorf("LogFormat = %q, want json", cfg.Telemetry.LogFormat)
	}
	if cfg.ShutdownGrace != defaultShutdownGrace {
		t.Errorf("ShutdownGrace = %s, want %s", cfg.ShutdownGrace, defaultShutdownGrace)
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
		{"malformed int64", "BLOOM_HTTP_MAX_BODY_BYTES", "not-a-number"},
		{"malformed duration", "BLOOM_HTTP_READ_TIMEOUT", "15"}, // no unit
		{"malformed bool", "BLOOM_DB_MIGRATE_ON_STARTUP", "maybe"},
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

func TestValidatePoolInvariants(t *testing.T) {
	base := func() Config {
		return Config{
			HTTP:     HTTPConfig{Addr: ":0", ReadHeaderTimeout: time.Second, MaxBodyBytes: 1},
			Database: DatabaseConfig{Driver: DriverSQLite, DSN: "file::memory:", MaxOpenConns: 5, MaxIdleConns: 5, ConnMaxLifetime: time.Minute},
			Telemetry: TelemetryConfig{
				LogFormat: LogFormatJSON, TraceSampleRatio: 1,
			},
			SecretKey:     NewSecret([]byte(testSecret)),
			ShutdownGrace: time.Second,
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("baseline Validate: %v", err)
	}

	bad := base()
	bad.Database.MaxIdleConns = 10
	if err := bad.Validate(); err == nil {
		t.Error("MaxIdleConns > MaxOpenConns accepted, want error")
	}
	bad = base()
	bad.Database.MaxOpenConns = 0
	if err := bad.Validate(); err == nil {
		t.Error("MaxOpenConns = 0 accepted, want error")
	}
	bad = base()
	bad.Database.ConnMaxLifetime = 0
	if err := bad.Validate(); err == nil {
		t.Error("ConnMaxLifetime = 0 accepted, want error")
	}
	bad = base()
	bad.ShutdownGrace = 0
	if err := bad.Validate(); err == nil {
		t.Error("ShutdownGrace = 0 accepted, want error")
	}
}
