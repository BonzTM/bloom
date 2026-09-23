// Package config loads, defaults, and validates all process configuration in
// one place. Precedence is flags > environment > hard-coded defaults, per the
// handbook's foundations/configuration.md. Validation is fail-fast: Load returns
// a fully validated Config or an actionable error, and main aborts before
// opening listeners or external clients.
//
// Every environment key carries the BLOOM_ prefix. There are no package-level
// globals and no init(): everything is wired through Load and passed
// explicitly. Every supported key is documented in .env.example and the README.
package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// Driver names the database engine, per ADR 0004: exactly two are supported.
type Driver string

const (
	// DriverSQLite selects the pure-Go modernc.org/sqlite driver (default).
	DriverSQLite Driver = "sqlite"
	// DriverPostgres selects the pure-Go pgx stdlib driver.
	DriverPostgres Driver = "postgres"
)

// LogFormat selects the slog handler.
type LogFormat string

const (
	// LogFormatJSON emits JSON records (production default).
	LogFormatJSON LogFormat = "json"
	// LogFormatText emits human-readable text records (local development).
	LogFormatText LogFormat = "text"
)

// Config is the closed, required-together set of process settings. It is built
// once by Load and threaded explicitly; it is never read from ambiently.
type Config struct {
	// HTTP holds the listener and server-hardening settings.
	HTTP HTTPConfig
	// Database holds the engine, connection string, and pool sizing.
	Database DatabaseConfig
	// Telemetry holds logging, tracing, and metrics configuration.
	Telemetry TelemetryConfig
	// Auth holds browser-session and login-rate-limit settings.
	Auth AuthConfig
	// Bootstrap holds the optional automatic first-administrator credentials.
	Bootstrap BootstrapConfig
	// SecretKey is the operator-supplied master secret (ADR 0006 item 6). It is
	// required and never logged: the Secret type redacts itself in every
	// formatting path.
	SecretKey Secret
	// Migrate selects the one-shot migration mode: apply the embedded goose
	// migrations against the configured database and exit instead of serving.
	// It is set by the -migrate flag only (no env key): it is an invocation mode,
	// not ambient configuration.
	Migrate bool
	// ShutdownGrace bounds ordered shutdown. It must exceed worst-case in-flight
	// work and stay under the platform termination grace.
	ShutdownGrace time.Duration
}

// BootstrapConfig configures the one-time first-administrator startup path.
type BootstrapConfig struct {
	// Username is the canonical username assigned when no account exists.
	Username string
	// Password enables startup bootstrap when it is non-empty. It never renders.
	Password Secret
}

// AuthConfig configures local login protection and server-side sessions.
type AuthConfig struct {
	// SessionCookieSecure controls the Secure cookie attribute. It defaults to
	// true and may be disabled only for plaintext local development.
	SessionCookieSecure bool
	// SessionLifetime is the absolute browser-session lifetime.
	SessionLifetime time.Duration
	// SessionIdleTimeout expires a browser session after inactivity.
	SessionIdleTimeout time.Duration
	// LoginRateRefillInterval adds one token to each login bucket per interval.
	LoginRateRefillInterval time.Duration
	// LoginRateBurst is the maximum tokens held by an IP or username bucket.
	LoginRateBurst int
	// LoginRateMaxKeys bounds the combined IP and username bucket map.
	LoginRateMaxKeys int
	// LoginMaxConcurrent bounds memory-intensive password verifications. Work
	// above this limit is rejected without queueing.
	LoginMaxConcurrent int
	// TrustedProxyCIDRs enables forwarded client addresses only for these peers.
	TrustedProxyCIDRs []netip.Prefix
}

// HTTPConfig configures the HTTP server and its hardening timeouts.
type HTTPConfig struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// ReadHeaderTimeout bounds how long reading request headers may take.
	ReadHeaderTimeout time.Duration
	// ReadTimeout bounds reading the entire request including the body.
	ReadTimeout time.Duration
	// WriteTimeout bounds writing the response. Disable only for streaming.
	WriteTimeout time.Duration
	// IdleTimeout bounds how long an idle keep-alive connection lives.
	IdleTimeout time.Duration
	// MaxBodyBytes caps non-streaming request bodies before decoding.
	MaxBodyBytes int64
}

// DatabaseConfig configures the engine and the database/sql pool. All four pool
// limits are set explicitly from config per the handbook's services/database.md;
// the defaults here are deliberate, not the database/sql zero values.
type DatabaseConfig struct {
	// Driver selects the engine: sqlite (default) or postgres.
	Driver Driver
	// DSN is the data source name. For sqlite an empty value takes
	// DefaultSQLiteDSN; for postgres it is required.
	DSN string
	// MaxOpenConns caps total open connections (server capacity / instances).
	MaxOpenConns int
	// MaxIdleConns is the idle floor; must be <= MaxOpenConns.
	MaxIdleConns int
	// ConnMaxLifetime bounds connection age (required behind LB/proxy/failover).
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime reaps idle connections so the pool shrinks under low load.
	ConnMaxIdleTime time.Duration
	// MigrateOnStartup applies pending goose migrations from the embedded FS
	// before the service accepts traffic. Off by default: production runs the
	// binary with -migrate as a separate step; a single-writer deployment may
	// enable this convenience.
	MigrateOnStartup bool
}

// TelemetryConfig configures structured logging, tracing, and the metrics seam.
type TelemetryConfig struct {
	// LogLevel is the minimum slog level.
	LogLevel slog.Level
	// LogFormat selects JSON output (production) or text (local dev).
	LogFormat LogFormat
	// OTLPEndpoint is the OTLP/HTTP trace collector endpoint (host:port, no
	// scheme), e.g. "otel-collector:4318". Empty disables span export: the
	// service installs a never-sampling provider so it runs offline.
	OTLPEndpoint string
	// OTLPInsecure sends spans over plaintext HTTP rather than TLS. Only for
	// in-cluster collectors / local development.
	OTLPInsecure bool
	// TraceSampleRatio is the head-based parent sampling ratio in [0,1]. 1.0
	// samples every trace; 0.0 samples none. Ignored when OTLPEndpoint is empty.
	TraceSampleRatio float64
}

// DefaultSQLiteDSN is the out-of-the-box SQLite DSN: a WAL-mode database file in
// the working directory with foreign-key enforcement on every connection, WAL
// mode, and a 5 second busy timeout (ADR 0004).
const DefaultSQLiteDSN = "file:bloom.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

// MinSecretKeyBytes is the minimum accepted BLOOM_SECRET_KEY length. 32 bytes is
// the smallest input that yields a full-strength 256-bit derived key.
const MinSecretKeyBytes = 32

// Default values. Kept as named constants so the defaults are a single,
// reviewable source of truth rather than scattered literals.
const (
	defaultAddr                    = ":8080"
	defaultReadHeaderTimeout       = 5 * time.Second
	defaultReadTimeout             = 15 * time.Second
	defaultWriteTimeout            = 15 * time.Second
	defaultIdleTimeout             = 60 * time.Second
	defaultMaxBodyBytes            = 1 << 20 // 1 MiB
	defaultMaxOpenConns            = 25
	defaultMaxIdleConns            = 25
	defaultConnMaxLifetime         = 30 * time.Minute
	defaultConnMaxIdleTime         = 5 * time.Minute
	defaultShutdownGrace           = 15 * time.Second
	defaultTraceSampleRatio        = 1.0
	defaultSessionLifetime         = 24 * time.Hour
	defaultSessionIdleTimeout      = 30 * time.Minute
	defaultLoginRateRefillInterval = time.Minute
	defaultLoginRateBurst          = 5
	defaultLoginRateMaxKeys        = 10_000
	maxLoginRateMaxKeys            = 100_000
	defaultLoginMaxConcurrent      = 4
	maxLoginMaxConcurrent          = 64
	defaultBootstrapUsername       = "admin"
)

// Load reads configuration from flags and the environment, applies defaults,
// and validates the result. Precedence is flags > environment > defaults: each
// flag's default is seeded from the environment (or the hard-coded default), so
// an explicit flag wins, an unset flag falls back to the env value, and an
// unset env falls back to the default.
//
// args are the process arguments excluding the program name (os.Args[1:]).
// Passing them in keeps Load testable without mutating global flag state.
func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("bloom", flag.ContinueOnError)
	env := newEnvReader()

	raw := bindFlags(fs, env)

	// Abort before parsing flags if any env value was malformed: a bad default
	// would otherwise be silently masked by an explicit flag or, worse, accepted.
	if err := env.err(); err != nil {
		return Config{}, err
	}
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	cfg, err := raw.build()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// rawFlags holds the parsed flag pointers between bindFlags and build. It exists
// so Load stays short and each step is testable in isolation.
type rawFlags struct {
	addr, dsn, driver, logLevel, logFormat, otlpEndpoint, secretKey *string
	bootstrapUsername                                               *string
	bootstrapPassword                                               string
	readHeaderTimeout, readTimeout, writeTimeout, idleTimeout       *time.Duration
	connMaxLifetime, connMaxIdleTime, shutdownGrace                 *time.Duration
	maxBodyBytes                                                    *int64
	maxOpenConns, maxIdleConns                                      *int
	migrateOnStartup, otlpInsecure, migrateMode                     *bool
	traceSampleRatio                                                *float64
	auth                                                            authRawFlags
}

type authRawFlags struct {
	trustedProxyCIDRs                   *string
	sessionCookieSecure                 *bool
	sessionLifetime, sessionIdleTimeout *time.Duration
	loginRateRefillInterval             *time.Duration
	loginRateBurst, loginRateMaxKeys    *int
	loginMaxConcurrent                  *int
}

// bindFlags declares every flag with its env-seeded default.
func bindFlags(fs *flag.FlagSet, env *envReader) rawFlags {
	return rawFlags{
		addr:              fs.String("http-addr", env.string("BLOOM_HTTP_ADDR", defaultAddr), "HTTP listen address"),
		readHeaderTimeout: fs.Duration("http-read-header-timeout", env.duration("BLOOM_HTTP_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout), "HTTP read-header timeout"),
		readTimeout:       fs.Duration("http-read-timeout", env.duration("BLOOM_HTTP_READ_TIMEOUT", defaultReadTimeout), "HTTP read timeout"),
		writeTimeout:      fs.Duration("http-write-timeout", env.duration("BLOOM_HTTP_WRITE_TIMEOUT", defaultWriteTimeout), "HTTP write timeout"),
		idleTimeout:       fs.Duration("http-idle-timeout", env.duration("BLOOM_HTTP_IDLE_TIMEOUT", defaultIdleTimeout), "HTTP idle timeout"),
		maxBodyBytes:      fs.Int64("http-max-body-bytes", env.int64("BLOOM_HTTP_MAX_BODY_BYTES", defaultMaxBodyBytes), "max request body size in bytes"),

		driver:           fs.String("db-driver", env.string("BLOOM_DB_DRIVER", string(DriverSQLite)), "database engine: sqlite|postgres"),
		dsn:              fs.String("db-dsn", env.string("BLOOM_DB_DSN", ""), "database DSN (sqlite default: "+DefaultSQLiteDSN+")"),
		maxOpenConns:     fs.Int("db-max-open-conns", env.int("BLOOM_DB_MAX_OPEN_CONNS", defaultMaxOpenConns), "max open DB connections"),
		maxIdleConns:     fs.Int("db-max-idle-conns", env.int("BLOOM_DB_MAX_IDLE_CONNS", defaultMaxIdleConns), "max idle DB connections"),
		connMaxLifetime:  fs.Duration("db-conn-max-lifetime", env.duration("BLOOM_DB_CONN_MAX_LIFETIME", defaultConnMaxLifetime), "max DB connection lifetime"),
		connMaxIdleTime:  fs.Duration("db-conn-max-idle-time", env.duration("BLOOM_DB_CONN_MAX_IDLE_TIME", defaultConnMaxIdleTime), "max DB connection idle time"),
		migrateOnStartup: fs.Bool("db-migrate-on-startup", env.bool("BLOOM_DB_MIGRATE_ON_STARTUP", false), "apply embedded goose migrations on startup"),

		secretKey: fs.String("secret-key", env.string("BLOOM_SECRET_KEY", ""), "master secret for at-rest encryption (required, >= 32 bytes; prefer the env var)"),
		bootstrapUsername: fs.String("bootstrap-username",
			env.string("BLOOM_BOOTSTRAP_USERNAME", defaultBootstrapUsername), "username for automatic first-administrator bootstrap"),
		bootstrapPassword: env.string("BLOOM_BOOTSTRAP_PASSWORD", ""),

		logLevel:         fs.String("log-level", env.string("BLOOM_LOG_LEVEL", "info"), "log level (debug|info|warn|error)"),
		logFormat:        fs.String("log-format", env.string("BLOOM_LOG_FORMAT", string(LogFormatJSON)), "log format (json|text)"),
		otlpEndpoint:     fs.String("otlp-endpoint", env.string("BLOOM_OTLP_ENDPOINT", ""), "OTLP/HTTP trace endpoint host:port (empty disables span export)"),
		otlpInsecure:     fs.Bool("otlp-insecure", env.bool("BLOOM_OTLP_INSECURE", false), "send spans over plaintext HTTP instead of TLS"),
		traceSampleRatio: fs.Float64("trace-sample-ratio", env.float64("BLOOM_TRACE_SAMPLE_RATIO", defaultTraceSampleRatio), "head-based trace sampling ratio in [0,1]"),
		auth:             bindAuthFlags(fs, env),

		// Deliberately flag-only (no env seed): -migrate is how a one-shot
		// migration Job invokes the binary, not a setting that varies by env.
		migrateMode:   fs.Bool("migrate", false, "apply the embedded goose migrations against the configured database and exit"),
		shutdownGrace: fs.Duration("shutdown-grace", env.duration("BLOOM_SHUTDOWN_GRACE", defaultShutdownGrace), "graceful shutdown budget"),
	}
}

func bindAuthFlags(fs *flag.FlagSet, env *envReader) authRawFlags {
	return authRawFlags{
		sessionCookieSecure:     fs.Bool("session-cookie-secure", env.bool("BLOOM_SESSION_COOKIE_SECURE", true), "set Secure on the session cookie"),
		sessionLifetime:         fs.Duration("session-lifetime", env.duration("BLOOM_SESSION_LIFETIME", defaultSessionLifetime), "absolute session lifetime"),
		sessionIdleTimeout:      fs.Duration("session-idle-timeout", env.duration("BLOOM_SESSION_IDLE_TIMEOUT", defaultSessionIdleTimeout), "session inactivity timeout"),
		loginRateRefillInterval: fs.Duration("login-rate-refill-interval", env.duration("BLOOM_LOGIN_RATE_REFILL_INTERVAL", defaultLoginRateRefillInterval), "login bucket token refill interval"),
		loginRateBurst:          fs.Int("login-rate-burst", env.int("BLOOM_LOGIN_RATE_BURST", defaultLoginRateBurst), "login bucket burst size"),
		loginRateMaxKeys:        fs.Int("login-rate-max-keys", env.int("BLOOM_LOGIN_RATE_MAX_KEYS", defaultLoginRateMaxKeys), "maximum tracked login rate-limit keys"),
		loginMaxConcurrent:      fs.Int("login-max-concurrent", env.int("BLOOM_LOGIN_MAX_CONCURRENT", defaultLoginMaxConcurrent), "maximum concurrent password verifications"),
		trustedProxyCIDRs:       fs.String("trusted-proxy-cidrs", env.string("BLOOM_TRUSTED_PROXY_CIDRS", ""), "comma-separated trusted reverse-proxy CIDRs"),
	}
}

// build converts parsed flag values into a typed Config, applying the
// driver-dependent DSN default and parsing enum-like strings.
func (r rawFlags) build() (Config, error) {
	level, err := parseLevel(*r.logLevel)
	if err != nil {
		return Config{}, err
	}
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs(*r.auth.trustedProxyCIDRs)
	if err != nil {
		return Config{}, err
	}
	bootstrap, err := r.buildBootstrap()
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTP:          r.buildHTTP(),
		Database:      r.buildDatabase(),
		Telemetry:     r.buildTelemetry(level),
		Auth:          r.auth.build(trustedProxyCIDRs),
		Bootstrap:     bootstrap,
		SecretKey:     NewSecret([]byte(*r.secretKey)),
		Migrate:       *r.migrateMode,
		ShutdownGrace: *r.shutdownGrace,
	}, nil
}

func (r rawFlags) buildHTTP() HTTPConfig {
	return HTTPConfig{
		Addr: *r.addr, ReadHeaderTimeout: *r.readHeaderTimeout, ReadTimeout: *r.readTimeout,
		WriteTimeout: *r.writeTimeout, IdleTimeout: *r.idleTimeout, MaxBodyBytes: *r.maxBodyBytes,
	}
}

func (r rawFlags) buildDatabase() DatabaseConfig {
	driver, dsn := Driver(*r.driver), *r.dsn
	if driver == DriverSQLite && dsn == "" {
		dsn = DefaultSQLiteDSN
	}
	return DatabaseConfig{
		Driver: driver, DSN: dsn, MaxOpenConns: *r.maxOpenConns, MaxIdleConns: *r.maxIdleConns,
		ConnMaxLifetime: *r.connMaxLifetime, ConnMaxIdleTime: *r.connMaxIdleTime,
		MigrateOnStartup: *r.migrateOnStartup,
	}
}

func (r rawFlags) buildTelemetry(level slog.Level) TelemetryConfig {
	return TelemetryConfig{
		LogLevel: level, LogFormat: LogFormat(*r.logFormat), OTLPEndpoint: *r.otlpEndpoint,
		OTLPInsecure: *r.otlpInsecure, TraceSampleRatio: *r.traceSampleRatio,
	}
}

func (r rawFlags) buildBootstrap() (BootstrapConfig, error) {
	username, err := core.CanonicalUsername(*r.bootstrapUsername)
	if err != nil {
		return BootstrapConfig{}, fmt.Errorf("config: BLOOM_BOOTSTRAP_USERNAME: %w", err)
	}
	return BootstrapConfig{Username: username, Password: NewSecret([]byte(r.bootstrapPassword))}, nil
}

func (r authRawFlags) build(trustedProxyCIDRs []netip.Prefix) AuthConfig {
	return AuthConfig{
		SessionCookieSecure: *r.sessionCookieSecure,
		SessionLifetime:     *r.sessionLifetime, SessionIdleTimeout: *r.sessionIdleTimeout,
		LoginRateRefillInterval: *r.loginRateRefillInterval, LoginRateBurst: *r.loginRateBurst,
		LoginRateMaxKeys: *r.loginRateMaxKeys, LoginMaxConcurrent: *r.loginMaxConcurrent,
		TrustedProxyCIDRs: trustedProxyCIDRs,
	}
}

func parseTrustedProxyCIDRs(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return nil, errors.New("config: BLOOM_TRUSTED_PROXY_CIDRS must contain at most 64 CIDRs")
	}
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("config: BLOOM_TRUSTED_PROXY_CIDRS contains %q: %w", part, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

// Validate enforces the invariants that must hold before the process opens
// listeners or external clients. It reports the first violation with an
// actionable message; it never silently corrects a bad value.
func (c Config) Validate() error {
	if err := c.HTTP.validate(); err != nil {
		return err
	}
	if err := c.Database.validate(); err != nil {
		return err
	}
	if err := c.Telemetry.validate(); err != nil {
		return err
	}
	if err := c.Auth.validate(); err != nil {
		return err
	}
	if err := c.Bootstrap.validate(); err != nil {
		return err
	}
	if c.SecretKey.Len() < MinSecretKeyBytes {
		return fmt.Errorf("config: BLOOM_SECRET_KEY must be set and at least %d bytes long (got %d)", MinSecretKeyBytes, c.SecretKey.Len())
	}
	if c.ShutdownGrace <= 0 {
		return fmt.Errorf("config: BLOOM_SHUTDOWN_GRACE must be positive, got %s", c.ShutdownGrace)
	}
	return nil
}

func (b BootstrapConfig) validate() error {
	if _, err := core.CanonicalUsername(b.Username); err != nil {
		return fmt.Errorf("config: BLOOM_BOOTSTRAP_USERNAME: %w", err)
	}
	return nil
}

func (a AuthConfig) validate() error {
	if a.SessionLifetime <= 0 {
		return fmt.Errorf("config: BLOOM_SESSION_LIFETIME must be positive, got %s", a.SessionLifetime)
	}
	if a.SessionIdleTimeout <= 0 || a.SessionIdleTimeout > a.SessionLifetime {
		return fmt.Errorf("config: BLOOM_SESSION_IDLE_TIMEOUT must be positive and <= BLOOM_SESSION_LIFETIME, got %s", a.SessionIdleTimeout)
	}
	if a.LoginRateRefillInterval <= 0 {
		return fmt.Errorf("config: BLOOM_LOGIN_RATE_REFILL_INTERVAL must be positive, got %s", a.LoginRateRefillInterval)
	}
	if a.LoginRateBurst <= 0 {
		return fmt.Errorf("config: BLOOM_LOGIN_RATE_BURST must be positive, got %d", a.LoginRateBurst)
	}
	if a.LoginRateMaxKeys < 2 {
		return fmt.Errorf("config: BLOOM_LOGIN_RATE_MAX_KEYS must be at least 2, got %d", a.LoginRateMaxKeys)
	}
	if a.LoginRateMaxKeys > maxLoginRateMaxKeys {
		return fmt.Errorf("config: BLOOM_LOGIN_RATE_MAX_KEYS must be <= %d, got %d", maxLoginRateMaxKeys, a.LoginRateMaxKeys)
	}
	if a.LoginMaxConcurrent <= 0 || a.LoginMaxConcurrent > maxLoginMaxConcurrent {
		return fmt.Errorf("config: BLOOM_LOGIN_MAX_CONCURRENT must be in [1,%d], got %d", maxLoginMaxConcurrent, a.LoginMaxConcurrent)
	}
	if len(a.TrustedProxyCIDRs) > 64 {
		return errors.New("config: BLOOM_TRUSTED_PROXY_CIDRS must contain at most 64 CIDRs")
	}
	for _, prefix := range a.TrustedProxyCIDRs {
		if !prefix.IsValid() {
			return errors.New("config: BLOOM_TRUSTED_PROXY_CIDRS contains an invalid CIDR")
		}
	}
	return nil
}

func (h HTTPConfig) validate() error {
	if h.Addr == "" {
		return errors.New("config: BLOOM_HTTP_ADDR must not be empty")
	}
	if h.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("config: BLOOM_HTTP_READ_HEADER_TIMEOUT must be positive, got %s", h.ReadHeaderTimeout)
	}
	if h.WriteTimeout <= 0 {
		return fmt.Errorf("config: BLOOM_HTTP_WRITE_TIMEOUT must be positive, got %s", h.WriteTimeout)
	}
	if h.MaxBodyBytes <= 0 {
		return fmt.Errorf("config: BLOOM_HTTP_MAX_BODY_BYTES must be positive, got %d", h.MaxBodyBytes)
	}
	return nil
}

func (d DatabaseConfig) validate() error {
	switch d.Driver {
	case DriverSQLite, DriverPostgres:
	default:
		return fmt.Errorf("config: BLOOM_DB_DRIVER must be %q or %q, got %q", DriverSQLite, DriverPostgres, d.Driver)
	}
	if d.DSN == "" {
		return fmt.Errorf("config: BLOOM_DB_DSN must be set when BLOOM_DB_DRIVER=%s", d.Driver)
	}
	if d.MaxOpenConns <= 0 {
		return fmt.Errorf("config: BLOOM_DB_MAX_OPEN_CONNS must be positive, got %d", d.MaxOpenConns)
	}
	// The pool invariant from the handbook's services/database.md: an idle floor
	// above the open cap is nonsensical and database/sql would silently clamp it.
	if d.MaxIdleConns > d.MaxOpenConns {
		return fmt.Errorf("config: BLOOM_DB_MAX_IDLE_CONNS (%d) must be <= BLOOM_DB_MAX_OPEN_CONNS (%d)", d.MaxIdleConns, d.MaxOpenConns)
	}
	if d.ConnMaxLifetime <= 0 {
		return fmt.Errorf("config: BLOOM_DB_CONN_MAX_LIFETIME must be positive, got %s", d.ConnMaxLifetime)
	}
	return nil
}

func (t TelemetryConfig) validate() error {
	switch t.LogFormat {
	case LogFormatJSON, LogFormatText:
	default:
		return fmt.Errorf("config: BLOOM_LOG_FORMAT must be %q or %q, got %q", LogFormatJSON, LogFormatText, t.LogFormat)
	}
	// Sampling ratio is a probability; an out-of-range value is operator error,
	// not something to silently clamp.
	if t.TraceSampleRatio < 0 || t.TraceSampleRatio > 1 {
		return fmt.Errorf("config: BLOOM_TRACE_SAMPLE_RATIO must be in [0,1], got %v", t.TraceSampleRatio)
	}
	return nil
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	// slog.Level implements encoding.TextUnmarshaler and accepts
	// debug/info/warn/error (case-insensitive).
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("config: invalid BLOOM_LOG_LEVEL %q: %w", s, err)
	}
	return level, nil
}
