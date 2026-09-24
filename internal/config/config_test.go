package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// testSecret is a 32-byte key that satisfies the minimum length.
const testSecret = "0123456789abcdef0123456789abcdef"

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("BLOOM_SECRET_KEY", testSecret)
}

func parseFlagOutput(args []string) (string, error) {
	var output strings.Builder
	fs := flag.NewFlagSet("bloom", flag.ContinueOnError)
	fs.SetOutput(&output)
	env := newEnvReader()
	_ = bindFlags(fs, env)
	if err := env.err(); err != nil {
		return output.String(), err
	}
	err := fs.Parse(args)
	return output.String(), err
}

func TestSecretFlagOutputNeverRevealsEnvironmentValues(t *testing.T) {
	const oidcSecret = "configured-oidc-client-secret"
	t.Setenv("BLOOM_SECRET_KEY", testSecret)
	t.Setenv("BLOOM_OIDC_CLIENT_SECRET", oidcSecret)
	tests := []struct {
		name string
		args []string
	}{
		{name: "usage", args: []string{"-h"}},
		{name: "parse error", args: []string{"-http-read-timeout=invalid"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := parseFlagOutput(testCase.args)
			if err == nil {
				t.Fatal("flag parsing succeeded, want error")
			}
			if testCase.name == "usage" && !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("usage error = %v, want flag.ErrHelp", err)
			}
			for _, secret := range []string{testSecret, oidcSecret} {
				if strings.Contains(output, secret) {
					t.Fatalf("%s output revealed a configured secret", testCase.name)
				}
			}
		})
	}
}

func TestSecretFlagsAreNotAccepted(t *testing.T) {
	t.Setenv("BLOOM_SECRET_KEY", testSecret)
	t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "configured-oidc-client-secret")
	for _, name := range []string{"secret-key", "oidc-client-secret"} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFlagOutput([]string{"-" + name, strings.Repeat("x", MinSecretKeyBytes)}); err == nil {
				t.Fatalf("-%s was accepted", name)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_HTTP_ADDR", ":9090")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	assertHTTPDefaults(t, cfg)
	assertDatabaseDefaults(t, cfg)
	assertAuthDefaults(t, cfg)
	assertOIDCDefaults(t, cfg)
	if cfg.Playback.PollActive != defaultPlaybackPollActive || cfg.Playback.PollIdle != defaultPlaybackPollIdle ||
		cfg.Playback.MissedPolls != defaultPlaybackMissedPolls ||
		cfg.Playback.ResumeWindow != defaultPlaybackResumeWindow ||
		cfg.Playback.StoreTimeout != defaultPlaybackStoreTimeout {
		t.Errorf("Playback defaults = %+v", cfg.Playback)
	}
	if cfg.Stats.CacheTTL != defaultStatsCacheTTL {
		t.Errorf("Stats cache TTL = %s, want %s", cfg.Stats.CacheTTL, defaultStatsCacheTTL)
	}
	if cfg.Bootstrap.Username != "admin" || cfg.Bootstrap.Password.Len() != 0 {
		t.Errorf("Bootstrap defaults = username %q password length %d", cfg.Bootstrap.Username, cfg.Bootstrap.Password.Len())
	}
	if cfg.ShutdownGrace != defaultShutdownGrace {
		t.Errorf("ShutdownGrace = %s, want %s", cfg.ShutdownGrace, defaultShutdownGrace)
	}
}

func assertHTTPDefaults(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.HTTP.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090 (env precedence)", cfg.HTTP.Addr)
	}
	if cfg.HTTP.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want default %d", cfg.HTTP.MaxBodyBytes, defaultMaxBodyBytes)
	}
}

func assertDatabaseDefaults(t *testing.T, cfg Config) {
	t.Helper()
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
}

func assertAuthDefaults(t *testing.T, cfg Config) {
	t.Helper()
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
}

func assertOIDCDefaults(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.PublicURL != "http://localhost:8080" {
		t.Errorf("PublicURL = %q, want local default", cfg.PublicURL)
	}
	if cfg.OIDC.Enabled {
		t.Error("OIDC.Enabled = true, want disabled")
	}
	if got := strings.Join(cfg.OIDC.Scopes, " "); got != "openid profile email" {
		t.Errorf("OIDC scopes = %q, want defaults", got)
	}
	if cfg.OIDC.UsernameClaim != "preferred_username" || cfg.OIDC.DefaultRole != "" {
		t.Errorf("OIDC claim defaults = username %q role %q", cfg.OIDC.UsernameClaim, cfg.OIDC.DefaultRole)
	}
}

func TestLoadOIDCConfiguration(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_PUBLIC_URL", "https://bloom.example")
	t.Setenv("BLOOM_OIDC_ENABLED", "true")
	t.Setenv("BLOOM_OIDC_ISSUER_URL", "https://id.example/application/o/bloom/")
	t.Setenv("BLOOM_OIDC_CLIENT_ID", "bloom")
	t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BLOOM_OIDC_REDIRECT_URL", "https://bloom.example/api/v1/auth/oidc/callback")
	t.Setenv("BLOOM_OIDC_ROLE_CLAIM", "groups")
	t.Setenv("BLOOM_OIDC_ROLE_MAP", "bloom-admins=owner,bloom-users=member")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OIDC.ClientSecret.String() != "[redacted]" || cfg.OIDC.ClientSecret.Len() != len("client-secret") {
		t.Fatal("OIDC client secret was not retained as a redacted Secret")
	}
	if rendered := fmt.Sprintf("%+v", cfg); strings.Contains(rendered, "client-secret") {
		t.Fatalf("rendered configuration leaks the OIDC client secret: %s", rendered)
	}
	if cfg.OIDC.RoleMap["bloom-admins"] != "owner" || cfg.OIDC.RoleMap["bloom-users"] != "member" {
		t.Fatalf("OIDC role map = %v", cfg.OIDC.RoleMap)
	}
}

func TestLoadRequestFulfilmentConfiguration(t *testing.T) {
	setRequired(t)
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if cfg.Requests.AvailabilitySource != AvailabilityMediaServer || cfg.Requests.AvailabilityInterval != 5*time.Minute {
		t.Fatalf("request defaults = %+v", cfg.Requests)
	}

	t.Setenv("BLOOM_REQUEST_AVAILABILITY_SOURCE", "download_manager")
	t.Setenv("BLOOM_REQUEST_AVAILABILITY_INTERVAL", "2m")
	cfg, err = Load(nil)
	if err != nil || cfg.Requests.AvailabilitySource != AvailabilityDownloadManager || cfg.Requests.AvailabilityInterval != 2*time.Minute {
		t.Fatalf("request settings = %+v, %v", cfg.Requests, err)
	}
}

func TestRequestFulfilmentConfigurationRejectsInvalidValues(t *testing.T) {
	tests := []struct{ key, value string }{
		{key: "BLOOM_REQUEST_AVAILABILITY_SOURCE", value: "queue"},
		{key: "BLOOM_REQUEST_AVAILABILITY_INTERVAL", value: "59s"},
		{key: "BLOOM_REQUEST_AVAILABILITY_INTERVAL", value: "25h"},
	}
	for _, testCase := range tests {
		t.Run(testCase.key+"="+testCase.value, func(t *testing.T) {
			setRequired(t)
			t.Setenv(testCase.key, testCase.value)
			if _, err := Load(nil); err == nil {
				t.Fatal("Load accepted invalid request fulfilment configuration")
			}
		})
	}
}

func TestLoadNotificationConfiguration(t *testing.T) {
	setRequired(t)
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if cfg.Notifications.Retention != 30*24*time.Hour || cfg.Notifications.WorkerInterval != 5*time.Second {
		t.Fatalf("notification defaults = %+v", cfg.Notifications)
	}
	t.Setenv("BLOOM_NOTIFY_RETENTION", "48h")
	t.Setenv("BLOOM_NOTIFY_WORKER_INTERVAL", "2s")
	cfg, err = Load(nil)
	if err != nil || cfg.Notifications.Retention != 48*time.Hour || cfg.Notifications.WorkerInterval != 2*time.Second {
		t.Fatalf("notification settings = %+v, %v", cfg.Notifications, err)
	}
}

func TestNotificationConfigurationRejectsInvalidValues(t *testing.T) {
	for _, testCase := range []struct{ key, value string }{
		{key: "BLOOM_NOTIFY_RETENTION", value: "0s"},
		{key: "BLOOM_NOTIFY_WORKER_INTERVAL", value: "999ms"},
		{key: "BLOOM_NOTIFY_WORKER_INTERVAL", value: "61m"},
	} {
		t.Run(testCase.key+"="+testCase.value, func(t *testing.T) {
			setRequired(t)
			t.Setenv(testCase.key, testCase.value)
			if _, err := Load(nil); err == nil {
				t.Fatal("Load accepted invalid notification configuration")
			}
		})
	}
}

func TestOIDCValidation(t *testing.T) {
	base := func(t *testing.T) {
		t.Helper()
		setRequired(t)
		t.Setenv("BLOOM_OIDC_ENABLED", "true")
		t.Setenv("BLOOM_PUBLIC_URL", "https://bloom.example")
		t.Setenv("BLOOM_OIDC_ISSUER_URL", "https://id.example")
		t.Setenv("BLOOM_OIDC_CLIENT_ID", "bloom")
		t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "client-secret")
		t.Setenv("BLOOM_OIDC_REDIRECT_URL", "https://bloom.example/api/v1/auth/oidc/callback")
	}
	tests := []struct {
		name, key, value string
	}{
		{name: "issuer requires https", key: "BLOOM_OIDC_ISSUER_URL", value: "http://id.example"},
		{name: "issuer no query", key: "BLOOM_OIDC_ISSUER_URL", value: "https://id.example?client=bloom"},
		{name: "issuer no empty query", key: "BLOOM_OIDC_ISSUER_URL", value: "https://id.example?"},
		{name: "issuer no fragment", key: "BLOOM_OIDC_ISSUER_URL", value: "https://id.example#metadata"},
		{name: "issuer no empty fragment", key: "BLOOM_OIDC_ISSUER_URL", value: "https://id.example#"},
		{name: "issuer not opaque", key: "BLOOM_OIDC_ISSUER_URL", value: "https:id.example"},
		{name: "issuer port only host", key: "BLOOM_OIDC_ISSUER_URL", value: "https://:443"},
		{name: "issuer empty host", key: "BLOOM_OIDC_ISSUER_URL", value: "https://"},
		{name: "issuer no userinfo", key: "BLOOM_OIDC_ISSUER_URL", value: "https://user@id.example"},
		{name: "issuer canonical round trip", key: "BLOOM_OIDC_ISSUER_URL", value: "https://id.example/a b"},
		{name: "redirect same origin", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://other.example/callback"},
		{name: "redirect exact path", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/callback"},
		{name: "redirect encoded path", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/%61pi/v1/auth/oidc/callback"},
		{name: "redirect encoded slash", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/api%2fv1/auth/oidc/callback"},
		{name: "redirect no query", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/api/v1/auth/oidc/callback?next=/"},
		{name: "redirect no empty query", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/api/v1/auth/oidc/callback?"},
		{name: "redirect no fragment", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/api/v1/auth/oidc/callback#next"},
		{name: "redirect no empty fragment", key: "BLOOM_OIDC_REDIRECT_URL", value: "https://bloom.example/api/v1/auth/oidc/callback#"},
		{name: "redirect https", key: "BLOOM_OIDC_REDIRECT_URL", value: "http://bloom.example/api/v1/auth/oidc/callback"},
		{name: "openid scope required", key: "BLOOM_OIDC_SCOPES", value: "profile email"},
		{name: "role claim required for map", key: "BLOOM_OIDC_ROLE_MAP", value: "admins=owner"},
		{name: "role map rejects controls", key: "BLOOM_OIDC_ROLE_MAP", value: "admins=own\x7fer"},
		{name: "bounded timeout", key: "BLOOM_OIDC_DISCOVERY_TIMEOUT", value: "31s"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			base(t)
			t.Setenv(testCase.key, testCase.value)
			_, err := Load(nil)
			if err == nil || !strings.Contains(err.Error(), testCase.key) {
				t.Fatalf("Load error = %v, want key %s", err, testCase.key)
			}
		})
	}
}

func TestOIDCConfigByteBounds(t *testing.T) {
	tests := []struct {
		name, key string
		atLimit   string
		overLimit string
		control   string
	}{
		{
			name: "public URL", key: "BLOOM_PUBLIC_URL", atLimit: "https://" + strings.Repeat("a", maxPublicURLBytes-len("https://")),
			overLimit: "https://" + strings.Repeat("a", maxPublicURLBytes-len("https://")+1), control: "https://bloom.example/\x01",
		},
		{
			name: "client id", key: "BLOOM_OIDC_CLIENT_ID", atLimit: strings.Repeat("i", maxOIDCClientIDBytes),
			overLimit: strings.Repeat("i", maxOIDCClientIDBytes+1), control: "client\x01id",
		},
		{
			name: "client secret", key: "BLOOM_OIDC_CLIENT_SECRET", atLimit: strings.Repeat("s", maxOIDCClientSecretBytes),
			overLimit: strings.Repeat("s", maxOIDCClientSecretBytes+1), control: "secret\x01",
		},
		{
			name: "scope", key: "BLOOM_OIDC_SCOPES", atLimit: "openid " + strings.Repeat("s", maxOIDCScopeBytes),
			overLimit: "openid " + strings.Repeat("s", maxOIDCScopeBytes+1), control: "openid group\x01name",
		},
		{
			name: "role claim", key: "BLOOM_OIDC_ROLE_CLAIM", atLimit: strings.Repeat("c", maxOIDCClaimNameBytes),
			overLimit: strings.Repeat("c", maxOIDCClaimNameBytes+1), control: "groups\x01",
		},
		{
			name: "role map claim", key: "BLOOM_OIDC_ROLE_MAP", atLimit: strings.Repeat("m", maxOIDCRoleMapClaimBytes) + "=owner",
			overLimit: strings.Repeat("m", maxOIDCRoleMapClaimBytes+1) + "=owner", control: "group\x01=owner",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			setValidOIDCEnvironment(t)
			t.Setenv(testCase.key, testCase.atLimit)
			if testCase.key == "BLOOM_PUBLIC_URL" {
				t.Setenv("BLOOM_OIDC_REDIRECT_URL", testCase.atLimit+"/api/v1/auth/oidc/callback")
			}
			if _, err := Load(nil); err != nil {
				t.Fatalf("Load at byte limit: %v", err)
			}
			t.Setenv(testCase.key, testCase.overLimit)
			if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), testCase.key) {
				t.Fatalf("Load over byte limit = %v, want %s error", err, testCase.key)
			}
			if testCase.control == "" {
				return
			}
			t.Setenv(testCase.key, testCase.control)
			if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), testCase.key) {
				t.Fatalf("Load control character = %v, want %s error", err, testCase.key)
			}
		})
	}
}

func TestOIDCRoleConfigurationUsesCoreRoleNameContract(t *testing.T) {
	roles := []string{
		"owner",
		strings.Repeat("r", core.MaxRoleNameBytes),
		strings.Repeat("r", core.MaxRoleNameBytes+1),
		" owner",
		"own\x7fer",
		string([]byte{0xff}),
	}
	for _, role := range roles {
		t.Run(fmt.Sprintf("%q", role), func(t *testing.T) {
			setValidOIDCEnvironment(t)
			t.Setenv("BLOOM_OIDC_DEFAULT_ROLE", role)
			_, err := Load(nil)
			if (err == nil) != core.ValidRoleName(role) {
				t.Fatalf("Load role %q error = %v, core.ValidRoleName = %v", role, err, core.ValidRoleName(role))
			}
		})
	}
}

func setValidOIDCEnvironment(t *testing.T) {
	t.Helper()
	setRequired(t)
	t.Setenv("BLOOM_OIDC_ENABLED", "true")
	t.Setenv("BLOOM_PUBLIC_URL", "https://bloom.example")
	t.Setenv("BLOOM_OIDC_ISSUER_URL", "https://id.example")
	t.Setenv("BLOOM_OIDC_CLIENT_ID", "bloom")
	t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BLOOM_OIDC_REDIRECT_URL", "https://bloom.example/api/v1/auth/oidc/callback")
	t.Setenv("BLOOM_OIDC_ROLE_CLAIM", "groups")
}

func TestOIDCIssuerLengthBound(t *testing.T) {
	base := func(t *testing.T) {
		t.Helper()
		setRequired(t)
		t.Setenv("BLOOM_OIDC_ENABLED", "true")
		t.Setenv("BLOOM_PUBLIC_URL", "https://bloom.example")
		t.Setenv("BLOOM_OIDC_CLIENT_ID", "bloom")
		t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "client-secret")
		t.Setenv("BLOOM_OIDC_REDIRECT_URL", "https://bloom.example/api/v1/auth/oidc/callback")
	}
	prefix := "https://id.example/"
	for _, testCase := range []struct {
		name   string
		length int
		valid  bool
	}{
		{name: "maximum", length: core.MaxOIDCIssuerBytes, valid: true},
		{name: "over maximum", length: core.MaxOIDCIssuerBytes + 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			base(t)
			t.Setenv("BLOOM_OIDC_ISSUER_URL", prefix+strings.Repeat("a", testCase.length-len(prefix)))
			_, err := Load(nil)
			if (err == nil) != testCase.valid {
				t.Fatalf("Load issuer length %d error = %v, want valid %v", testCase.length, err, testCase.valid)
			}
		})
	}
}

func TestOIDCLoopbackHTTPRequiresDevelopmentFlag(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_OIDC_ENABLED", "true")
	t.Setenv("BLOOM_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("BLOOM_OIDC_ISSUER_URL", "http://127.0.0.1:9090")
	t.Setenv("BLOOM_OIDC_CLIENT_ID", "bloom")
	t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BLOOM_OIDC_REDIRECT_URL", "http://localhost:8080/api/v1/auth/oidc/callback")
	if _, err := Load(nil); err == nil {
		t.Fatal("Load accepted loopback HTTP issuer without development flag")
	}
	t.Setenv("BLOOM_OIDC_ALLOW_INSECURE_ISSUER", "true")
	if _, err := Load(nil); err != nil {
		t.Fatalf("Load rejected explicitly enabled loopback issuer: %v", err)
	}
}

func TestOIDCLoopbackRedirectHTTPRequiresDevelopmentFlag(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_OIDC_ENABLED", "true")
	t.Setenv("BLOOM_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("BLOOM_OIDC_ISSUER_URL", "https://id.example")
	t.Setenv("BLOOM_OIDC_CLIENT_ID", "bloom")
	t.Setenv("BLOOM_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BLOOM_OIDC_REDIRECT_URL", "http://localhost:8080/api/v1/auth/oidc/callback")
	if _, err := Load(nil); err == nil {
		t.Fatal("Load accepted loopback HTTP redirect without development flag")
	}
	t.Setenv("BLOOM_OIDC_ALLOW_INSECURE_ISSUER", "true")
	if _, err := Load(nil); err != nil {
		t.Fatalf("Load rejected explicitly enabled loopback redirect: %v", err)
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

func TestLoadPlaybackConfigFromEnvironment(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_PLAYBACK_POLL_ACTIVE", "7s")
	t.Setenv("BLOOM_PLAYBACK_POLL_IDLE", "45s")
	t.Setenv("BLOOM_PLAYBACK_MISSED_POLLS", "4")
	t.Setenv("BLOOM_PLAYBACK_RESUME_WINDOW", "8m")
	t.Setenv("BLOOM_PLAYBACK_STORE_TIMEOUT", "3s")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := PlaybackConfig{
		PollActive: 7 * time.Second, PollIdle: 45 * time.Second,
		MissedPolls: 4, ResumeWindow: 8 * time.Minute, StoreTimeout: 3 * time.Second,
	}
	if cfg.Playback != want {
		t.Fatalf("Playback = %+v, want %+v", cfg.Playback, want)
	}
}

func TestLoadRejectsPlaybackBounds(t *testing.T) {
	tests := []struct{ key, value string }{
		{key: "BLOOM_PLAYBACK_POLL_ACTIVE", value: "999ms"},
		{key: "BLOOM_PLAYBACK_POLL_ACTIVE", value: "61s"},
		{key: "BLOOM_PLAYBACK_POLL_IDLE", value: "4s"},
		{key: "BLOOM_PLAYBACK_POLL_IDLE", value: "11m"},
		{key: "BLOOM_PLAYBACK_MISSED_POLLS", value: "0"},
		{key: "BLOOM_PLAYBACK_MISSED_POLLS", value: "101"},
		{key: "BLOOM_PLAYBACK_RESUME_WINDOW", value: "999ms"},
		{key: "BLOOM_PLAYBACK_RESUME_WINDOW", value: "25h"},
		{key: "BLOOM_PLAYBACK_STORE_TIMEOUT", value: "99ms"},
		{key: "BLOOM_PLAYBACK_STORE_TIMEOUT", value: "31s"},
	}
	for _, testCase := range tests {
		t.Run(testCase.key+"="+testCase.value, func(t *testing.T) {
			setRequired(t)
			t.Setenv(testCase.key, testCase.value)
			_, err := Load(nil)
			if err == nil || !strings.Contains(err.Error(), testCase.key) {
				t.Fatalf("Load error = %v, want %s validation", err, testCase.key)
			}
		})
	}
}

func TestLoadStatsCacheTTL(t *testing.T) {
	setRequired(t)
	t.Setenv("BLOOM_STATS_CACHE_TTL", "0")
	cfg, err := Load(nil)
	if err != nil || cfg.Stats.CacheTTL != 0 {
		t.Fatalf("Load disabled stats cache = %s, %v", cfg.Stats.CacheTTL, err)
	}
	t.Setenv("BLOOM_STATS_CACHE_TTL", "-1s")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "BLOOM_STATS_CACHE_TTL") {
		t.Fatalf("Load negative stats cache TTL = %v", err)
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
		{"malformed playback duration", "BLOOM_PLAYBACK_POLL_ACTIVE", "fast"},
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
		Playback: PlaybackConfig{
			PollActive: defaultPlaybackPollActive, PollIdle: defaultPlaybackPollIdle,
			MissedPolls: defaultPlaybackMissedPolls, ResumeWindow: defaultPlaybackResumeWindow,
			StoreTimeout: defaultPlaybackStoreTimeout,
		},
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

// A disabled provider still bounds every populated value, so switching it
// on later cannot make a malformed value live.
func TestOIDCDisabledStillBoundsPopulatedValues(t *testing.T) {
	tests := []struct {
		name, key, value string
	}{
		{name: "oversized client id", key: "BLOOM_OIDC_CLIENT_ID", value: strings.Repeat("a", maxOIDCClientIDBytes+1)},
		{name: "control in display name", key: "BLOOM_OIDC_DISPLAY_NAME", value: "Home\x01SSO"},
		{name: "oversized scope", key: "BLOOM_OIDC_SCOPES", value: "openid " + strings.Repeat("s", maxOIDCScopeBytes+1)},
		{name: "invalid utf8 role claim", key: "BLOOM_OIDC_ROLE_CLAIM", value: "roles\xff"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("BLOOM_OIDC_ENABLED", "false")
			t.Setenv(test.key, test.value)
			if _, err := Load(nil); err == nil {
				t.Fatalf("Load accepted %s=%q with OIDC disabled", test.key, test.value)
			}
		})
	}
	t.Run("empty values pass", func(t *testing.T) {
		setRequired(t)
		t.Setenv("BLOOM_OIDC_ENABLED", "false")
		if _, err := Load(nil); err != nil {
			t.Fatalf("Load with OIDC disabled: %v", err)
		}
	})
}
