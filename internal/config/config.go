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
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	// PublicURL is the externally visible Bloom origin used to validate OAuth
	// redirects. It contains no path, query, or fragment.
	PublicURL string
	// OIDC configures the optional generic OpenID Connect sign-in provider.
	OIDC OIDCConfig
	// Playback configures adaptive media-server session polling.
	Playback PlaybackConfig
	// Stats configures the bounded dashboard result cache.
	Stats StatsConfig
	// Requests configures fulfilment availability polling.
	Requests RequestFulfilmentConfig
	// SecretKey is the operator-supplied master secret (ADR 0006 item 6). It is
	// required and never logged: the Secret type redacts itself in every
	// formatting path.
	SecretKey Secret
	// Migrate selects the one-shot migration mode: apply the embedded goose
	// migrations against the configured database and exit instead of serving.
	// It is set by the -migrate flag only (no env key): it is an invocation mode,
	// not ambient configuration.
	Migrate bool
	// ShutdownGrace bounds ordered shutdown. HTTP drain receives half, so this
	// must exceed twice the longest request and stay under platform grace.
	ShutdownGrace time.Duration
}

// BootstrapConfig configures the one-time first-administrator startup path.
type BootstrapConfig struct {
	// Username is the canonical username assigned when no account exists.
	Username string
	// Password enables startup bootstrap when it is non-empty. It never renders.
	Password Secret
}

// OIDCConfig configures one generic OpenID Connect provider.
type OIDCConfig struct {
	Enabled              bool
	DisplayName          string
	IssuerURL            string
	ClientID             string
	ClientSecret         Secret
	RedirectURL          string
	Scopes               []string
	UsernameClaim        string
	RoleClaim            string
	RoleMap              map[string]string
	DefaultRole          string
	AllowInsecureIssuer  bool
	DiscoveryTimeout     time.Duration
	TokenExchangeTimeout time.Duration
	JWKSFetchTimeout     time.Duration
}

// PlaybackConfig configures playback collection lifecycle bounds.
type PlaybackConfig struct {
	PollActive   time.Duration
	PollIdle     time.Duration
	MissedPolls  int
	ResumeWindow time.Duration
	StoreTimeout time.Duration
}

// StatsConfig configures statistics result caching.
type StatsConfig struct {
	// CacheTTL bounds dashboard staleness. Zero disables caching.
	CacheTTL time.Duration
}

// AvailabilitySource selects the authority for request availability.
type AvailabilitySource string

const (
	// AvailabilityMediaServer uses registered media servers as the availability authority.
	AvailabilityMediaServer AvailabilitySource = "media_server"
	// AvailabilityDownloadManager uses the selected download manager as the availability authority.
	AvailabilityDownloadManager AvailabilitySource = "download_manager"
)

// RequestFulfilmentConfig controls the processing-request availability poller.
type RequestFulfilmentConfig struct {
	AvailabilityInterval time.Duration
	AvailabilitySource   AvailabilitySource
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
	defaultAddr                        = ":8080"
	defaultReadHeaderTimeout           = 5 * time.Second
	defaultReadTimeout                 = 15 * time.Second
	defaultWriteTimeout                = 15 * time.Second
	defaultIdleTimeout                 = 60 * time.Second
	defaultMaxBodyBytes                = 1 << 20 // 1 MiB
	defaultMaxOpenConns                = 25
	defaultMaxIdleConns                = 25
	defaultConnMaxLifetime             = 30 * time.Minute
	defaultConnMaxIdleTime             = 5 * time.Minute
	defaultShutdownGrace               = 15 * time.Second
	defaultTraceSampleRatio            = 1.0
	defaultSessionLifetime             = 24 * time.Hour
	defaultSessionIdleTimeout          = 30 * time.Minute
	defaultLoginRateRefillInterval     = time.Minute
	defaultLoginRateBurst              = 5
	defaultLoginRateMaxKeys            = 10_000
	maxLoginRateMaxKeys                = 100_000
	defaultLoginMaxConcurrent          = 4
	maxLoginMaxConcurrent              = 64
	defaultBootstrapUsername           = "admin"
	defaultPublicURL                   = "http://localhost:8080"
	defaultOIDCDisplayName             = "OpenID Connect"
	defaultOIDCScopes                  = "openid profile email"
	defaultOIDCUsernameClaim           = "preferred_username"
	defaultOIDCDefaultRole             = ""
	defaultOIDCTimeout                 = 5 * time.Second
	minOIDCTimeout                     = 100 * time.Millisecond
	maxOIDCTimeout                     = 30 * time.Second
	maxPublicURLBytes                  = 2048
	maxOIDCClientIDBytes               = 512
	maxOIDCClientSecretBytes           = 4096
	maxOIDCScopeBytes                  = 512
	maxOIDCClaimNameBytes              = 512
	maxOIDCRoleMapClaimBytes           = 512
	defaultPlaybackPollActive          = 5 * time.Second
	defaultPlaybackPollIdle            = 30 * time.Second
	defaultPlaybackMissedPolls         = 3
	defaultPlaybackResumeWindow        = 5 * time.Minute
	defaultPlaybackStoreTimeout        = 5 * time.Second
	defaultStatsCacheTTL               = 30 * time.Second
	minPlaybackPollActive              = time.Second
	maxPlaybackPollActive              = time.Minute
	minPlaybackPollIdle                = 5 * time.Second
	maxPlaybackPollIdle                = 10 * time.Minute
	maxPlaybackMissedPolls             = 100
	minPlaybackResumeWindow            = time.Second
	maxPlaybackResumeWindow            = 24 * time.Hour
	minPlaybackStoreTimeout            = 100 * time.Millisecond
	maxPlaybackStoreTimeout            = 30 * time.Second
	defaultRequestAvailabilityInterval = 5 * time.Minute
	minRequestAvailabilityInterval     = time.Minute
	maxRequestAvailabilityInterval     = 24 * time.Hour
)

// Load reads configuration from flags and the environment, applies defaults,
// and validates the result. Precedence is flags > environment > defaults for
// non-secret settings. Secrets load from the environment only so they never
// enter process arguments or flag output.
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
	addr, dsn, driver, logLevel, logFormat, otlpEndpoint, publicURL *string
	bootstrapUsername                                               *string
	secretKey, bootstrapPassword                                    string
	readHeaderTimeout, readTimeout, writeTimeout, idleTimeout       *time.Duration
	connMaxLifetime, connMaxIdleTime, shutdownGrace                 *time.Duration
	maxBodyBytes                                                    *int64
	maxOpenConns, maxIdleConns                                      *int
	migrateOnStartup, otlpInsecure, migrateMode                     *bool
	traceSampleRatio                                                *float64
	auth                                                            authRawFlags
	oidc                                                            oidcRawFlags
	playback                                                        playbackRawFlags
	stats                                                           statsRawFlags
	requests                                                        requestRawFlags
}

type authRawFlags struct {
	trustedProxyCIDRs                   *string
	sessionCookieSecure                 *bool
	sessionLifetime, sessionIdleTimeout *time.Duration
	loginRateRefillInterval             *time.Duration
	loginRateBurst, loginRateMaxKeys    *int
	loginMaxConcurrent                  *int
}

type oidcRawFlags struct {
	enabled, allowInsecureIssuer                        *bool
	displayName, issuerURL, clientID                    *string
	clientSecret                                        string
	redirectURL, scopes, usernameClaim, roleClaim       *string
	roleMap, defaultRole                                *string
	discoveryTimeout, tokenExchangeTimeout, jwksTimeout *time.Duration
}

type playbackRawFlags struct {
	pollActive, pollIdle, resumeWindow *time.Duration
	storeTimeout                       *time.Duration
	missedPolls                        *int
}

type requestRawFlags struct {
	availabilityInterval *time.Duration
	availabilitySource   *string
}

type statsRawFlags struct{ cacheTTL *time.Duration }

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

		secretKey: env.string("BLOOM_SECRET_KEY", ""),
		bootstrapUsername: fs.String("bootstrap-username",
			env.string("BLOOM_BOOTSTRAP_USERNAME", defaultBootstrapUsername), "username for automatic first-administrator bootstrap"),
		bootstrapPassword: env.string("BLOOM_BOOTSTRAP_PASSWORD", ""),
		publicURL:         fs.String("public-url", env.string("BLOOM_PUBLIC_URL", defaultPublicURL), "externally visible Bloom origin"),

		logLevel:         fs.String("log-level", env.string("BLOOM_LOG_LEVEL", "info"), "log level (debug|info|warn|error)"),
		logFormat:        fs.String("log-format", env.string("BLOOM_LOG_FORMAT", string(LogFormatJSON)), "log format (json|text)"),
		otlpEndpoint:     fs.String("otlp-endpoint", env.string("BLOOM_OTLP_ENDPOINT", ""), "OTLP/HTTP trace endpoint host:port (empty disables span export)"),
		otlpInsecure:     fs.Bool("otlp-insecure", env.bool("BLOOM_OTLP_INSECURE", false), "send spans over plaintext HTTP instead of TLS"),
		traceSampleRatio: fs.Float64("trace-sample-ratio", env.float64("BLOOM_TRACE_SAMPLE_RATIO", defaultTraceSampleRatio), "head-based trace sampling ratio in [0,1]"),
		auth:             bindAuthFlags(fs, env),
		oidc:             bindOIDCFlags(fs, env),
		playback:         bindPlaybackFlags(fs, env),
		stats:            bindStatsFlags(fs, env),
		requests:         bindRequestFlags(fs, env),

		// Deliberately flag-only (no env seed): -migrate is how a one-shot
		// migration Job invokes the binary, not a setting that varies by env.
		migrateMode:   fs.Bool("migrate", false, "apply the embedded goose migrations against the configured database and exit"),
		shutdownGrace: fs.Duration("shutdown-grace", env.duration("BLOOM_SHUTDOWN_GRACE", defaultShutdownGrace), "graceful shutdown budget"),
	}
}

func bindStatsFlags(fs *flag.FlagSet, env *envReader) statsRawFlags {
	return statsRawFlags{cacheTTL: fs.Duration("stats-cache-ttl", env.duration(
		"BLOOM_STATS_CACHE_TTL", defaultStatsCacheTTL), "statistics result cache TTL (0 disables)")}
}

func bindRequestFlags(fs *flag.FlagSet, env *envReader) requestRawFlags {
	return requestRawFlags{
		availabilityInterval: fs.Duration("request-availability-interval", env.duration(
			"BLOOM_REQUEST_AVAILABILITY_INTERVAL", defaultRequestAvailabilityInterval), "request availability poll interval"),
		availabilitySource: fs.String("request-availability-source", env.string(
			"BLOOM_REQUEST_AVAILABILITY_SOURCE", string(AvailabilityMediaServer)), "availability source: media_server|download_manager"),
	}
}

func bindPlaybackFlags(fs *flag.FlagSet, env *envReader) playbackRawFlags {
	return playbackRawFlags{
		pollActive: fs.Duration("playback-poll-active", env.duration(
			"BLOOM_PLAYBACK_POLL_ACTIVE", defaultPlaybackPollActive), "active playback poll interval"),
		pollIdle: fs.Duration("playback-poll-idle", env.duration(
			"BLOOM_PLAYBACK_POLL_IDLE", defaultPlaybackPollIdle), "idle playback poll interval"),
		missedPolls: fs.Int("playback-missed-polls", env.int(
			"BLOOM_PLAYBACK_MISSED_POLLS", defaultPlaybackMissedPolls), "polls missed before a watch stops"),
		resumeWindow: fs.Duration("playback-resume-window", env.duration(
			"BLOOM_PLAYBACK_RESUME_WINDOW", defaultPlaybackResumeWindow), "window for reopening a stopped watch"),
		storeTimeout: fs.Duration("playback-store-timeout", env.duration(
			"BLOOM_PLAYBACK_STORE_TIMEOUT", defaultPlaybackStoreTimeout), "playback store operation timeout"),
	}
}

func bindOIDCFlags(fs *flag.FlagSet, env *envReader) oidcRawFlags {
	return oidcRawFlags{
		enabled:              fs.Bool("oidc-enabled", env.bool("BLOOM_OIDC_ENABLED", false), "enable OpenID Connect sign-in"),
		displayName:          fs.String("oidc-display-name", env.string("BLOOM_OIDC_DISPLAY_NAME", defaultOIDCDisplayName), "OIDC provider display name"),
		issuerURL:            fs.String("oidc-issuer-url", env.string("BLOOM_OIDC_ISSUER_URL", ""), "OIDC issuer URL"),
		clientID:             fs.String("oidc-client-id", env.string("BLOOM_OIDC_CLIENT_ID", ""), "OIDC client id"),
		clientSecret:         env.string("BLOOM_OIDC_CLIENT_SECRET", ""),
		redirectURL:          fs.String("oidc-redirect-url", env.string("BLOOM_OIDC_REDIRECT_URL", ""), "OIDC callback URL"),
		scopes:               fs.String("oidc-scopes", env.string("BLOOM_OIDC_SCOPES", defaultOIDCScopes), "space-separated OIDC scopes"),
		usernameClaim:        fs.String("oidc-username-claim", env.string("BLOOM_OIDC_USERNAME_CLAIM", defaultOIDCUsernameClaim), "OIDC username claim"),
		roleClaim:            fs.String("oidc-role-claim", env.string("BLOOM_OIDC_ROLE_CLAIM", ""), "optional OIDC role claim"),
		roleMap:              fs.String("oidc-role-map", env.string("BLOOM_OIDC_ROLE_MAP", ""), "claim-value to Bloom-role mappings"),
		defaultRole:          fs.String("oidc-default-role", env.string("BLOOM_OIDC_DEFAULT_ROLE", defaultOIDCDefaultRole), "role assigned to newly provisioned accounts"),
		allowInsecureIssuer:  fs.Bool("oidc-allow-insecure-issuer", env.bool("BLOOM_OIDC_ALLOW_INSECURE_ISSUER", false), "allow HTTP issuer on loopback for development"),
		discoveryTimeout:     fs.Duration("oidc-discovery-timeout", env.duration("BLOOM_OIDC_DISCOVERY_TIMEOUT", defaultOIDCTimeout), "OIDC discovery timeout"),
		tokenExchangeTimeout: fs.Duration("oidc-token-exchange-timeout", env.duration("BLOOM_OIDC_TOKEN_EXCHANGE_TIMEOUT", defaultOIDCTimeout), "OIDC token exchange timeout"),
		jwksTimeout:          fs.Duration("oidc-jwks-fetch-timeout", env.duration("BLOOM_OIDC_JWKS_FETCH_TIMEOUT", defaultOIDCTimeout), "OIDC JWKS fetch timeout"),
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
	roleMap, err := parseOIDCRoleMap(*r.oidc.roleMap)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTP:          r.httpConfig(),
		Database:      r.databaseConfig(),
		Telemetry:     r.telemetryConfig(level),
		Auth:          r.authConfig(trustedProxyCIDRs),
		Bootstrap:     bootstrap,
		PublicURL:     *r.publicURL,
		OIDC:          r.oidcConfig(roleMap),
		Playback:      r.playbackConfig(),
		Stats:         StatsConfig{CacheTTL: *r.stats.cacheTTL},
		Requests:      r.requestConfig(),
		SecretKey:     NewSecret([]byte(r.secretKey)),
		Migrate:       *r.migrateMode,
		ShutdownGrace: *r.shutdownGrace,
	}, nil
}

func (r rawFlags) requestConfig() RequestFulfilmentConfig {
	return RequestFulfilmentConfig{
		AvailabilityInterval: *r.requests.availabilityInterval,
		AvailabilitySource:   AvailabilitySource(*r.requests.availabilitySource),
	}
}

func (r rawFlags) playbackConfig() PlaybackConfig {
	return PlaybackConfig{
		PollActive: *r.playback.pollActive, PollIdle: *r.playback.pollIdle,
		MissedPolls: *r.playback.missedPolls, ResumeWindow: *r.playback.resumeWindow,
		StoreTimeout: *r.playback.storeTimeout,
	}
}

func (r rawFlags) httpConfig() HTTPConfig {
	return HTTPConfig{
		Addr: *r.addr, ReadHeaderTimeout: *r.readHeaderTimeout,
		ReadTimeout: *r.readTimeout, WriteTimeout: *r.writeTimeout,
		IdleTimeout: *r.idleTimeout, MaxBodyBytes: *r.maxBodyBytes,
	}
}

func (r rawFlags) databaseConfig() DatabaseConfig {
	driver, dsn := Driver(*r.driver), *r.dsn
	if driver == DriverSQLite && dsn == "" {
		dsn = DefaultSQLiteDSN
	}
	return DatabaseConfig{
		Driver: driver, DSN: dsn, MaxOpenConns: *r.maxOpenConns,
		MaxIdleConns: *r.maxIdleConns, ConnMaxLifetime: *r.connMaxLifetime,
		ConnMaxIdleTime: *r.connMaxIdleTime, MigrateOnStartup: *r.migrateOnStartup,
	}
}

func (r rawFlags) telemetryConfig(level slog.Level) TelemetryConfig {
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

func (r rawFlags) authConfig(prefixes []netip.Prefix) AuthConfig {
	return AuthConfig{
		SessionCookieSecure: *r.auth.sessionCookieSecure,
		SessionLifetime:     *r.auth.sessionLifetime, SessionIdleTimeout: *r.auth.sessionIdleTimeout,
		LoginRateRefillInterval: *r.auth.loginRateRefillInterval,
		LoginRateBurst:          *r.auth.loginRateBurst, LoginRateMaxKeys: *r.auth.loginRateMaxKeys,
		LoginMaxConcurrent: *r.auth.loginMaxConcurrent, TrustedProxyCIDRs: prefixes,
	}
}

func (r rawFlags) oidcConfig(roleMap map[string]string) OIDCConfig {
	return OIDCConfig{
		Enabled: *r.oidc.enabled, DisplayName: *r.oidc.displayName,
		IssuerURL: *r.oidc.issuerURL, ClientID: *r.oidc.clientID,
		ClientSecret: NewSecret([]byte(r.oidc.clientSecret)), RedirectURL: *r.oidc.redirectURL,
		Scopes: strings.Fields(*r.oidc.scopes), UsernameClaim: *r.oidc.usernameClaim,
		RoleClaim: *r.oidc.roleClaim, RoleMap: roleMap, DefaultRole: *r.oidc.defaultRole,
		AllowInsecureIssuer: *r.oidc.allowInsecureIssuer, DiscoveryTimeout: *r.oidc.discoveryTimeout,
		TokenExchangeTimeout: *r.oidc.tokenExchangeTimeout, JWKSFetchTimeout: *r.oidc.jwksTimeout,
	}
}

func parseOIDCRoleMap(raw string) (map[string]string, error) {
	result := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return nil, errors.New("config: BLOOM_OIDC_ROLE_MAP must contain at most 64 mappings")
	}
	for _, part := range parts {
		claim, role, ok := strings.Cut(part, "=")
		claim, role = strings.TrimSpace(claim), strings.TrimSpace(role)
		if !ok || validateConfigText(claim, maxOIDCRoleMapClaimBytes) != nil || !core.ValidRoleName(role) {
			return nil, fmt.Errorf("config: BLOOM_OIDC_ROLE_MAP contains invalid mapping %q", part)
		}
		if _, exists := result[claim]; exists {
			return nil, fmt.Errorf("config: BLOOM_OIDC_ROLE_MAP repeats claim value %q", claim)
		}
		result[claim] = role
	}
	return result, nil
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
	if err := c.Playback.validate(); err != nil {
		return err
	}
	if c.Stats.CacheTTL < 0 {
		return fmt.Errorf("config: BLOOM_STATS_CACHE_TTL must be at least 0, got %s", c.Stats.CacheTTL)
	}
	if err := c.Requests.validate(); err != nil {
		return err
	}
	if err := c.Bootstrap.validate(); err != nil {
		return err
	}
	if c.PublicURL != "" || c.OIDC.Enabled {
		publicURL, err := validatePublicURL(c.PublicURL)
		if err != nil {
			return err
		}
		if err := c.OIDC.validate(publicURL); err != nil {
			return err
		}
	}
	if c.SecretKey.Len() < MinSecretKeyBytes {
		return fmt.Errorf("config: BLOOM_SECRET_KEY must be set and at least %d bytes long (got %d)", MinSecretKeyBytes, c.SecretKey.Len())
	}
	if c.ShutdownGrace <= 0 {
		return fmt.Errorf("config: BLOOM_SHUTDOWN_GRACE must be positive, got %s", c.ShutdownGrace)
	}
	return nil
}

func (r RequestFulfilmentConfig) validate() error {
	if r.AvailabilityInterval == 0 && r.AvailabilitySource == "" {
		return nil
	}
	if r.AvailabilityInterval < minRequestAvailabilityInterval || r.AvailabilityInterval > maxRequestAvailabilityInterval {
		return fmt.Errorf("config: BLOOM_REQUEST_AVAILABILITY_INTERVAL must be between %s and %s", minRequestAvailabilityInterval, maxRequestAvailabilityInterval)
	}
	if r.AvailabilitySource != AvailabilityMediaServer && r.AvailabilitySource != AvailabilityDownloadManager {
		return errors.New("config: BLOOM_REQUEST_AVAILABILITY_SOURCE must be media_server or download_manager")
	}
	return nil
}

func (p PlaybackConfig) validate() error {
	if p.PollActive < minPlaybackPollActive || p.PollActive > maxPlaybackPollActive {
		return fmt.Errorf("config: BLOOM_PLAYBACK_POLL_ACTIVE must be in [%s,%s], got %s",
			minPlaybackPollActive, maxPlaybackPollActive, p.PollActive)
	}
	if p.PollIdle < minPlaybackPollIdle || p.PollIdle > maxPlaybackPollIdle {
		return fmt.Errorf("config: BLOOM_PLAYBACK_POLL_IDLE must be in [%s,%s], got %s",
			minPlaybackPollIdle, maxPlaybackPollIdle, p.PollIdle)
	}
	if p.MissedPolls < 1 || p.MissedPolls > maxPlaybackMissedPolls {
		return fmt.Errorf("config: BLOOM_PLAYBACK_MISSED_POLLS must be in [1,%d], got %d",
			maxPlaybackMissedPolls, p.MissedPolls)
	}
	if p.ResumeWindow < minPlaybackResumeWindow || p.ResumeWindow > maxPlaybackResumeWindow {
		return fmt.Errorf("config: BLOOM_PLAYBACK_RESUME_WINDOW must be in [%s,%s], got %s",
			minPlaybackResumeWindow, maxPlaybackResumeWindow, p.ResumeWindow)
	}
	if p.StoreTimeout < minPlaybackStoreTimeout || p.StoreTimeout > maxPlaybackStoreTimeout {
		return fmt.Errorf("config: BLOOM_PLAYBACK_STORE_TIMEOUT must be in [%s,%s], got %s",
			minPlaybackStoreTimeout, maxPlaybackStoreTimeout, p.StoreTimeout)
	}
	return nil
}

func (b BootstrapConfig) validate() error {
	if _, err := core.CanonicalUsername(b.Username); err != nil {
		return fmt.Errorf("config: BLOOM_BOOTSTRAP_USERNAME: %w", err)
	}
	return nil
}

func validatePublicURL(raw string) (*url.URL, error) {
	if err := validateConfigText(raw, maxPublicURLBytes); err != nil {
		return nil, fmt.Errorf("config: BLOOM_PUBLIC_URL must contain 1-%d valid bytes without control characters", maxPublicURLBytes)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.Path != "" || parsed.RawPath != "" || parsed.ForceQuery ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("config: BLOOM_PUBLIC_URL must be an absolute HTTP(S) origin, got %q", raw)
	}
	return parsed, nil
}

func (o OIDCConfig) validate(publicURL *url.URL) error {
	if !o.Enabled {
		return nil
	}
	if o.DisplayName == "" || len(o.DisplayName) > 80 {
		return errors.New("config: BLOOM_OIDC_DISPLAY_NAME must contain 1-80 bytes")
	}
	if validateConfigText(o.IssuerURL, core.MaxOIDCIssuerBytes) != nil {
		return fmt.Errorf("config: BLOOM_OIDC_ISSUER_URL must contain 1-%d valid bytes without control characters", core.MaxOIDCIssuerBytes)
	}
	if err := o.validateIssuer(); err != nil {
		return err
	}
	if err := validateConfigText(o.ClientID, maxOIDCClientIDBytes); err != nil {
		return fmt.Errorf("config: BLOOM_OIDC_CLIENT_ID must contain 1-%d valid bytes without control characters", maxOIDCClientIDBytes)
	}
	if validateConfigText(string(o.ClientSecret.Bytes()), maxOIDCClientSecretBytes) != nil {
		return fmt.Errorf("config: BLOOM_OIDC_CLIENT_SECRET must contain 1-%d valid bytes without control characters", maxOIDCClientSecretBytes)
	}
	if err := o.validateRedirect(publicURL); err != nil {
		return err
	}
	if err := o.validateClaims(); err != nil {
		return err
	}
	return o.validateTimeouts()
}

func (o OIDCConfig) validateIssuer() error {
	issuer, err := url.Parse(o.IssuerURL)
	if err != nil || issuer.Hostname() == "" || issuer.User != nil || issuer.Opaque != "" || issuer.ForceQuery ||
		issuer.RawQuery != "" || issuer.Fragment != "" || issuer.RawFragment != "" || issuer.String() != o.IssuerURL {
		return errors.New("config: BLOOM_OIDC_ISSUER_URL must be an absolute canonical issuer URL without query or fragment")
	}
	if issuer.Scheme != "https" && (!o.AllowInsecureIssuer || issuer.Scheme != "http" || !loopbackHost(issuer.Hostname())) {
		return errors.New("config: BLOOM_OIDC_ISSUER_URL must use HTTPS; HTTP is allowed only on loopback with BLOOM_OIDC_ALLOW_INSECURE_ISSUER=true")
	}
	return nil
}

func (o OIDCConfig) validateRedirect(publicURL *url.URL) error {
	redirect, err := url.Parse(o.RedirectURL)
	expectedRedirect := *publicURL
	expectedRedirect.Path = "/api/v1/auth/oidc/callback"
	if err != nil || redirect.User != nil || redirect.Opaque != "" || redirect.RawPath != "" || redirect.ForceQuery ||
		redirect.RawQuery != "" || redirect.Fragment != "" || redirect.RawFragment != "" ||
		o.RedirectURL != expectedRedirect.String() {
		return errors.New("config: BLOOM_OIDC_REDIRECT_URL must be the configured public origin plus /api/v1/auth/oidc/callback without query or fragment")
	}
	if redirect.Scheme != "https" && (!o.AllowInsecureIssuer || redirect.Scheme != "http" || !loopbackHost(redirect.Hostname())) {
		return errors.New("config: BLOOM_OIDC_REDIRECT_URL must use HTTPS; HTTP is allowed only on loopback with BLOOM_OIDC_ALLOW_INSECURE_ISSUER=true")
	}
	return nil
}

func (o OIDCConfig) validateClaims() error {
	if len(o.Scopes) == 0 || len(o.Scopes) > 16 || !slices.Contains(o.Scopes, "openid") {
		return errors.New("config: BLOOM_OIDC_SCOPES must contain openid and at most 16 scopes")
	}
	for _, scope := range o.Scopes {
		if validateConfigText(scope, maxOIDCScopeBytes) != nil {
			return fmt.Errorf("config: BLOOM_OIDC_SCOPES entries must contain 1-%d valid bytes without control characters", maxOIDCScopeBytes)
		}
	}
	if validateConfigText(o.UsernameClaim, maxOIDCClaimNameBytes) != nil {
		return fmt.Errorf("config: BLOOM_OIDC_USERNAME_CLAIM must contain 1-%d valid bytes without control characters", maxOIDCClaimNameBytes)
	}
	if o.RoleClaim != "" && validateConfigText(o.RoleClaim, maxOIDCClaimNameBytes) != nil {
		return fmt.Errorf("config: BLOOM_OIDC_ROLE_CLAIM must not exceed %d valid bytes or contain control characters", maxOIDCClaimNameBytes)
	}
	if len(o.RoleMap) > 0 && o.RoleClaim == "" {
		return errors.New("config: BLOOM_OIDC_ROLE_MAP requires BLOOM_OIDC_ROLE_CLAIM")
	}
	if o.DefaultRole != "" && !core.ValidRoleName(o.DefaultRole) {
		return errors.New("config: BLOOM_OIDC_DEFAULT_ROLE is not a valid role name")
	}
	return nil
}

func validateConfigText(value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) ||
		strings.IndexFunc(value, unicode.IsControl) != -1 {
		return core.ErrInvalidText
	}
	return nil
}

func (o OIDCConfig) validateTimeouts() error {
	values := []struct {
		key   string
		value time.Duration
	}{
		{"BLOOM_OIDC_DISCOVERY_TIMEOUT", o.DiscoveryTimeout},
		{"BLOOM_OIDC_TOKEN_EXCHANGE_TIMEOUT", o.TokenExchangeTimeout},
		{"BLOOM_OIDC_JWKS_FETCH_TIMEOUT", o.JWKSFetchTimeout},
	}
	for _, value := range values {
		if value.value < minOIDCTimeout || value.value > maxOIDCTimeout {
			return fmt.Errorf("config: %s must be in [%s,%s], got %s", value.key, minOIDCTimeout, maxOIDCTimeout, value.value)
		}
	}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
