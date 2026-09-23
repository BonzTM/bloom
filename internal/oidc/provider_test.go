package oidc_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	adapter "github.com/BonzTM/bloom/internal/oidc"
	"github.com/BonzTM/bloom/internal/testutil"
)

var oidcFixtureNow = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

type fakeProvider struct {
	server        *httptest.Server
	key           *rsa.PrivateKey
	badKey        *rsa.PrivateKey
	mu            sync.Mutex
	claims        map[string]any
	kid           string
	jwksHits      int
	verifier      string
	tokenStarted  chan struct{}
	tokenRelease  chan struct{}
	tokenHits     int
	tokenStatus   int
	tokenError    string
	tokenBody     string
	tokenAuth     string
	omitIDToken   bool
	algorithm     string
	jwksStatus    int
	jwksBody      string
	jwksRedirect  string
	jwksStarted   chan struct{}
	jwksRelease   chan struct{}
	jwksCanceled  chan struct{}
	tokenRedirect string
	tokenLarge    bool
	discoveryAlgs []string
	tokenMethods  []string
}

type recordingMetrics struct {
	mu     sync.Mutex
	events map[string]int
}

func (m *recordingMetrics) ObserveOIDCDependency(operation, outcome string, _ float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.events == nil {
		m.events = make(map[string]int)
	}
	m.events[operation+":"+outcome]++
}

func (m *recordingMetrics) count(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events[key]
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	return newFakeProviderWithConnState(t, nil)
}

func newFakeProviderWithConnState(t *testing.T, connState func(net.Conn, http.ConnState)) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	badKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate second RSA key: %v", err)
	}
	fake := &fakeProvider{
		key: key, badKey: badKey, kid: "test-key",
		discoveryAlgs: []string{"RS256"}, tokenMethods: []string{"client_secret_basic"},
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fake.serveHTTP))
	server.Config.ConnState = connState
	server.Start()
	fake.server = server
	t.Cleanup(fake.server.Close)
	fake.resetClaims("expected-nonce")
	return fake
}

func (f *fakeProvider) resetClaims(nonce string) {
	f.claims = map[string]any{
		"iss": f.server.URL, "aud": "bloom", "sub": "subject-1",
		"exp": oidcFixtureNow.Add(time.Hour).Unix(), "iat": oidcFixtureNow.Add(-time.Minute).Unix(),
		"nonce": nonce, "preferred_username": "alice", "groups": []string{"users"},
	}
	f.kid = "test-key"
}

func (f *fakeProvider) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		f.mu.Lock()
		algorithms := slices.Clone(f.discoveryAlgs)
		f.mu.Unlock()
		metadata := map[string]any{
			"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize",
			"token_endpoint": f.server.URL + "/token", "jwks_uri": f.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": algorithms,
		}
		f.mu.Lock()
		if f.tokenMethods != nil {
			metadata["token_endpoint_auth_methods_supported"] = slices.Clone(f.tokenMethods)
		}
		f.mu.Unlock()
		mustEncode(w, metadata)
	case "/token":
		f.serveToken(w, r)
	case "/jwks":
		f.mu.Lock()
		f.jwksHits++
		f.mu.Unlock()
		if f.jwksStarted != nil {
			f.jwksStarted <- struct{}{}
		}
		if f.jwksCanceled != nil {
			<-r.Context().Done()
			close(f.jwksCanceled)
			return
		}
		if f.jwksRelease != nil {
			select {
			case <-f.jwksRelease:
			case <-r.Context().Done():
				return
			}
		}
		if f.jwksRedirect != "" {
			http.Redirect(w, r, f.jwksRedirect, http.StatusTemporaryRedirect)
			return
		}
		if f.jwksStatus != 0 {
			http.Error(w, "unavailable", f.jwksStatus)
			return
		}
		if f.jwksBody != "" {
			if _, err := w.Write([]byte(f.jwksBody)); err != nil {
				panic(err)
			}
			return
		}
		mustEncode(w, map[string]any{"keys": []any{jwk("test-key", &f.key.PublicKey)}})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeProvider) serveToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.tokenHits++
	started := f.tokenStarted
	release := f.tokenRelease
	errorCode := f.tokenError
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return
	}
	if f.tokenRedirect != "" {
		http.Redirect(w, r, f.tokenRedirect, http.StatusTemporaryRedirect)
		return
	}
	if f.tokenStatus != 0 {
		if errorCode != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.tokenStatus)
			mustEncode(w, map[string]string{"error": errorCode})
			return
		}
		http.Error(w, "provider failure", f.tokenStatus)
		return
	}
	if f.tokenBody != "" {
		if _, err := w.Write([]byte(f.tokenBody)); err != nil {
			panic(err)
		}
		return
	}
	if f.tokenLarge {
		if _, err := w.Write([]byte(`{"padding":"` + strings.Repeat("x", maxTestResponseBytes) + `"}`)); err != nil {
			panic(err)
		}
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	f.writeSuccessfulToken(w, r)
}

func (f *fakeProvider) writeSuccessfulToken(w http.ResponseWriter, r *http.Request) {
	username, password, basic := r.BasicAuth()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.verifier = r.Form.Get("code_verifier")
	switch {
	case basic && username == "bloom" && password == "secret":
		f.tokenAuth = "client_secret_basic"
	case r.Form.Get("client_id") == "bloom" && r.Form.Get("client_secret") == "secret":
		f.tokenAuth = "client_secret_post"
	default:
		f.tokenAuth = "invalid"
	}
	claims := cloneClaims(f.claims)
	kid := f.kid
	f.mu.Unlock()
	key := f.key
	if kid == "bad-signature" {
		key = f.badKey
		kid = "test-key"
	}
	algorithm := f.algorithm
	if algorithm == "" {
		algorithm = "RS256"
	}
	token, err := signTokenWithAlgorithm(key, kid, algorithm, claims)
	if err != nil {
		http.Error(w, "sign failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"access_token": "not-stored", "refresh_token": "not-stored",
		"token_type": "Bearer", "expires_in": 300, "id_token": token,
	}
	if f.omitIDToken {
		delete(response, "id_token")
	}
	mustEncode(w, response)
}

func TestProviderDiscoveryRetriesAreBounded(t *testing.T) {
	var attempts atomic.Int32
	metrics := &recordingMetrics{}
	var delays []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	_, err := adapter.New(context.Background(), testConfig(server.URL, time.Hour), adapter.Dependencies{
		Metrics: metrics,
		Random:  func(limit time.Duration) (time.Duration, error) { return limit / 2, nil },
		Wait: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	})
	if err == nil {
		t.Fatal("New succeeded against unavailable discovery endpoint")
	}
	if attempts.Load() != 3 {
		t.Fatalf("discovery attempts = %d, want 3", attempts.Load())
	}
	if metrics.count("discovery:retry") != 2 || metrics.count("discovery:exhausted") != 1 {
		t.Fatalf("discovery retry metrics = %v", metrics.events)
	}
	if got := fmt.Sprint(delays); got != "[50ms 100ms]" {
		t.Fatalf("discovery retry delays = %s, want [50ms 100ms]", got)
	}
}

func TestProviderClosesDiscoveryTransportOnFailure(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	server.Config.ConnState = connectionCloseSignal(closed)
	server.Start()
	t.Cleanup(server.Close)
	_, err := adapter.New(t.Context(), testConfig(server.URL, time.Second), adapter.Dependencies{
		Random: func(time.Duration) (time.Duration, error) { return 0, nil },
		Wait:   func(context.Context, time.Duration) error { return nil },
	})
	if err == nil {
		t.Fatal("New succeeded against unavailable discovery endpoint")
	}
	waitForConnectionClose(t, closed)
}

func TestProviderCloseReleasesLongLivedTransports(t *testing.T) {
	closed := make(chan struct{}, 3)
	fake := newFakeProviderWithConnState(t, connectionCloseSignal(closed))
	provider := newAdapter(t, fake)
	waitForConnectionClose(t, closed)
	if _, err := provider.Exchange(t.Context(), "code", strings.Repeat("v", 43), "expected-nonce"); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForConnectionClose(t, closed)
	waitForConnectionClose(t, closed)
}

func TestProviderCloseCancelsActiveSigningKeyFetch(t *testing.T) {
	fake := newFakeProvider(t)
	fake.jwksStarted = make(chan struct{}, 1)
	fake.jwksCanceled = make(chan struct{})
	provider := newAdapter(t, fake)
	exchangeDone := make(chan error, 1)
	go func() {
		_, err := provider.Exchange(t.Context(), "code", strings.Repeat("v", 43), "expected-nonce")
		exchangeDone <- err
	}()
	<-fake.jwksStarted
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-fake.jwksCanceled
	if err := <-exchangeDone; err == nil ||
		errors.Is(err, core.ErrOIDCProviderUnavailable) || errors.Is(err, core.ErrOIDCRejected) {
		t.Fatalf("Exchange error = %v, want opaque cancellation", err)
	}
}

func connectionCloseSignal(closed chan<- struct{}) func(net.Conn, http.ConnState) {
	return func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
}

func waitForConnectionClose(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("transport connection did not close")
	}
}

func TestProviderDiscoveryRejectsRedirectWithoutContactingTarget(t *testing.T) {
	var sourceHits, targetHits atomic.Int32
	var source *httptest.Server
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		mustEncode(w, map[string]any{
			"issuer": source.URL, "authorization_endpoint": source.URL + "/authorize",
			"token_endpoint": source.URL + "/token", "jwks_uri": source.URL + "/jwks",
		})
	}))
	t.Cleanup(target.Close)
	source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHits.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	_, err := adapter.New(context.Background(), testConfig(source.URL, time.Second), adapter.Dependencies{
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if !errors.Is(err, core.ErrOIDCProviderUnavailable) {
		t.Fatalf("New redirect error = %v, want provider unavailable", err)
	}
	if sourceHits.Load() != 1 || targetHits.Load() != 0 {
		t.Fatalf("redirect hits = source %d target %d, want 1 and 0", sourceHits.Load(), targetHits.Load())
	}
}

func TestProviderRejectsInsecureDiscoveredEndpoint(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mustEncode(w, map[string]any{
			"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize",
			"token_endpoint": "http://identity.example/token", "jwks_uri": server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(server.Close)
	if _, err := adapter.New(context.Background(), testConfig(server.URL, time.Second)); err == nil {
		t.Fatal("New accepted a non-loopback plaintext token endpoint")
	}
}

func TestProviderRejectsMissingOrUnsupportedDiscoveryAlgorithms(t *testing.T) {
	tests := []struct {
		name       string
		algorithms []string
	}{
		{name: "missing"},
		{name: "unsupported", algorithms: []string{"HS256", "none"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			fake.mu.Lock()
			fake.discoveryAlgs = testCase.algorithms
			fake.mu.Unlock()
			if _, err := adapter.New(context.Background(), testConfig(fake.server.URL, time.Second)); err == nil {
				t.Fatal("New accepted discovery metadata without a supported signing algorithm")
			}
		})
	}
}

func TestProviderOutboundCancellation(t *testing.T) {
	fake := newFakeProvider(t)
	provider := newAdapter(t, fake)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	fake.mu.Lock()
	fake.tokenStarted = started
	fake.tokenRelease = release
	fake.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.Exchange(ctx, "code", strings.Repeat("v", 43), "expected-nonce")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil || errors.Is(err, core.ErrOIDCProviderUnavailable) || errors.Is(err, core.ErrOIDCRejected) {
		t.Fatalf("Exchange error = %v, want opaque caller cancellation", err)
	}
	close(release)
}

const maxTestResponseBytes = (1 << 20) + 1

func testConfig(issuer string, timeout time.Duration) config.OIDCConfig {
	return config.OIDCConfig{
		Enabled: true, IssuerURL: issuer, ClientID: "bloom",
		ClientSecret: config.NewSecret([]byte("secret")), RedirectURL: "http://bloom.test/callback",
		Scopes: []string{"openid"}, UsernameClaim: "preferred_username", AllowInsecureIssuer: true,
		DiscoveryTimeout: timeout, TokenExchangeTimeout: timeout, JWKSFetchTimeout: timeout,
	}
}

func cloneClaims(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	maps.Copy(result, source)
	return result
}

func mustEncode(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

func jwk(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func jwkDocument(t *testing.T, key map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"keys": []any{key}})
	if err != nil {
		t.Fatalf("marshal JWK document: %v", err)
	}
	return string(encoded)
}

func signTokenWithAlgorithm(key *rsa.PrivateKey, kid, algorithm string, claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": algorithm, "kid": kid, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func newAdapter(t *testing.T, fake *fakeProvider) *adapter.Provider {
	t.Helper()
	cfg := testConfig(fake.server.URL, time.Second)
	cfg.Scopes = []string{"openid", "profile"}
	cfg.RoleClaim = "groups"
	provider, err := adapter.New(context.Background(), cfg, adapter.Dependencies{
		Clock: testutil.NewFakeClock(oidcFixtureNow),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cleanupAdapter(t, provider)
	return provider
}

func cleanupAdapter(t *testing.T, provider *adapter.Provider) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := provider.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

func TestProviderSelectsDiscoveredTokenAuthentication(t *testing.T) {
	tests := []struct {
		name    string
		methods []string
		want    string
	}{
		{name: "basic preferred", methods: []string{"client_secret_post", "client_secret_basic"}, want: "client_secret_basic"},
		{name: "post accepted", methods: []string{"client_secret_post"}, want: "client_secret_post"},
		{name: "omitted defaults basic", methods: nil, want: "client_secret_basic"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			fake.tokenMethods = testCase.methods
			provider := newAdapter(t, fake)
			if _, err := provider.Exchange(t.Context(), "code", strings.Repeat("v", 43), "expected-nonce"); err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if fake.tokenAuth != testCase.want {
				t.Fatalf("token authentication = %q, want %q", fake.tokenAuth, testCase.want)
			}
		})
	}
}

func TestProviderRejectsUnsupportedTokenAuthentication(t *testing.T) {
	fake := newFakeProvider(t)
	fake.tokenMethods = []string{"private_key_jwt", "none"}
	if _, err := adapter.New(t.Context(), testConfig(fake.server.URL, time.Second)); err == nil {
		t.Fatal("New accepted unsupported token endpoint authentication")
	}
}

func TestProviderAuthorizationAndExchange(t *testing.T) {
	fake := newFakeProvider(t)
	provider := newAdapter(t, fake)
	verifier := strings.Repeat("v", 43)
	authURL, err := url.Parse(provider.AuthorizationURL("state", "expected-nonce", oauth2.S256ChallengeFromVerifier(verifier)))
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if authURL.Query().Get("state") != "state" || authURL.Query().Get("nonce") != "expected-nonce" ||
		authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization query = %v", authURL.Query())
	}
	claims, err := provider.Exchange(context.Background(), "code", verifier, "expected-nonce")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if claims.Issuer != fake.server.URL || claims.Subject != "subject-1" || claims.Username != "alice" || len(claims.RoleValues) != 1 {
		t.Fatalf("claims = %+v", claims)
	}
	if fake.verifier != verifier {
		t.Fatalf("token verifier = %q, want PKCE verifier", fake.verifier)
	}
}

func TestProviderInstrumentsOutboundOperations(t *testing.T) {
	fake := newFakeProvider(t)
	metrics := &recordingMetrics{}
	cfg := testConfig(fake.server.URL, time.Second)
	cfg.RoleClaim = "groups"
	provider, err := adapter.New(context.Background(), cfg, adapter.Dependencies{
		Clock: testutil.NewFakeClock(oidcFixtureNow), Metrics: metrics,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cleanupAdapter(t, provider)
	if _, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce"); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	for _, key := range []string{"discovery:request_success", "token_exchange:request_success", "jwks:request_success"} {
		if metrics.count(key) == 0 {
			t.Errorf("missing metric event %q: %v", key, metrics.events)
		}
	}
}

func TestProviderRejectsInvalidIDTokens(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeProvider)
		nonce  string
		stage  string
	}{
		{name: "issuer", mutate: func(f *fakeProvider) { f.claims["iss"] = "https://wrong.example" }, nonce: "expected-nonce"},
		{name: "audience", mutate: func(f *fakeProvider) { f.claims["aud"] = "other" }, nonce: "expected-nonce"},
		{name: "nonce", mutate: func(f *fakeProvider) { f.claims["nonce"] = "wrong" }, nonce: "expected-nonce"},
		{name: "expiry", mutate: func(f *fakeProvider) { f.claims["exp"] = oidcFixtureNow.Add(-time.Hour).Unix() }, nonce: "expected-nonce"},
		{name: "empty subject", mutate: func(f *fakeProvider) { f.claims["sub"] = "" }, nonce: "expected-nonce", stage: "identity"},
		{name: "oversized subject", mutate: func(f *fakeProvider) { f.claims["sub"] = strings.Repeat("s", 513) }, nonce: "expected-nonce", stage: "identity"},
		{name: "unsupported algorithm", mutate: func(f *fakeProvider) { f.algorithm = "HS256" }, nonce: "expected-nonce"},
		{name: "none algorithm", mutate: func(f *fakeProvider) { f.algorithm = "none" }, nonce: "expected-nonce"},
		{name: "signature", mutate: func(f *fakeProvider) { f.kid = "bad-signature" }, nonce: "expected-nonce"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			testCase.mutate(fake)
			provider := newAdapter(t, fake)
			_, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), testCase.nonce)
			if !errors.Is(err, core.ErrOIDCRejected) {
				t.Fatalf("Exchange error = %v, want terminal rejection", err)
			}
			if rejection, ok := errors.AsType[*core.OIDCRejection](err); testCase.stage != "" && (!ok || rejection.Stage != testCase.stage) {
				t.Fatalf("Exchange rejection = %+v, want stage %q", rejection, testCase.stage)
			}
		})
	}
}

func TestProviderRejectsJWKAlgorithmAndUseMismatch(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "algorithm", field: "alg", value: "RS384"},
		{name: "encryption use", field: "use", value: "enc"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			key := jwk("test-key", &fake.key.PublicKey)
			key[testCase.field] = testCase.value
			fake.jwksBody = jwkDocument(t, key)
			_, err := newAdapter(t, fake).Exchange(
				context.Background(), "code", strings.Repeat("v", 43), "expected-nonce",
			)
			if err == nil {
				t.Fatal("Exchange accepted a JWK whose metadata forbids token verification")
			}
		})
	}
}

func TestProviderValidatesAuthorizedParty(t *testing.T) {
	tests := []struct {
		name string
		azp  any
		ok   bool
	}{
		{name: "missing", ok: false},
		{name: "wrong", azp: "another-client", ok: false},
		{name: "matching", azp: "bloom", ok: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			fake.claims["aud"] = []string{"bloom", "api"}
			if testCase.azp != nil {
				fake.claims["azp"] = testCase.azp
			}
			_, err := newAdapter(t, fake).Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce")
			if (err == nil) != testCase.ok {
				t.Fatalf("Exchange error = %v, want success %v", err, testCase.ok)
			}
		})
	}
}

func TestProviderValidatesAuthorizedPartyForSingleAudience(t *testing.T) {
	tests := []struct {
		name string
		azp  any
	}{
		{name: "wrong", azp: "another-client"},
		{name: "empty", azp: ""},
		{name: "malformed type", azp: []string{"bloom"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			fake.claims["azp"] = testCase.azp
			_, err := newAdapter(t, fake).Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce")
			if !errors.Is(err, core.ErrOIDCRejected) {
				t.Fatalf("Exchange error = %v, want terminal rejection", err)
			}
		})
	}
}

func TestProviderRejectsMalformedAndOversizedClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeProvider)
	}{
		{name: "username type", mutate: func(f *fakeProvider) { f.claims["preferred_username"] = 42 }},
		{name: "username bytes", mutate: func(f *fakeProvider) { f.claims["preferred_username"] = strings.Repeat("u", 1025) }},
		{name: "role type", mutate: func(f *fakeProvider) { f.claims["groups"] = map[string]string{"role": "users"} }},
		{name: "role count", mutate: func(f *fakeProvider) { f.claims["groups"] = make([]string, 65) }},
		{name: "role bytes", mutate: func(f *fakeProvider) { f.claims["groups"] = []string{strings.Repeat("r", 257)} }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			testCase.mutate(fake)
			if _, err := newAdapter(t, fake).Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce"); !errors.Is(err, core.ErrOIDCRejected) {
				t.Fatalf("Exchange error = %v, want terminal rejection", err)
			}
		})
	}
}

func TestProviderClassifiesTokenAndJWKSFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeProvider)
		want   error
		opaque bool
	}{
		{name: "missing id token", mutate: func(f *fakeProvider) { f.omitIDToken = true }, want: core.ErrOIDCRejected},
		{name: "invalid grant", mutate: func(f *fakeProvider) {
			f.tokenStatus, f.tokenError = http.StatusBadRequest, "invalid_grant"
		}, want: core.ErrOIDCRejected},
		{name: "invalid client", mutate: func(f *fakeProvider) {
			f.tokenStatus, f.tokenError = http.StatusUnauthorized, "invalid_client"
		}, opaque: true},
		{name: "unauthorized client", mutate: func(f *fakeProvider) {
			f.tokenStatus, f.tokenError = http.StatusBadRequest, "unauthorized_client"
		}, opaque: true},
		{name: "token 404", mutate: func(f *fakeProvider) { f.tokenStatus = http.StatusNotFound }, opaque: true},
		{name: "token 429", mutate: func(f *fakeProvider) { f.tokenStatus = http.StatusTooManyRequests }, want: core.ErrOIDCProviderUnavailable},
		{name: "token 500", mutate: func(f *fakeProvider) { f.tokenStatus = http.StatusInternalServerError }, opaque: true},
		{name: "token 502", mutate: func(f *fakeProvider) { f.tokenStatus = http.StatusBadGateway }, want: core.ErrOIDCProviderUnavailable},
		{name: "token 503", mutate: func(f *fakeProvider) { f.tokenStatus = http.StatusServiceUnavailable }, want: core.ErrOIDCProviderUnavailable},
		{name: "token 504", mutate: func(f *fakeProvider) { f.tokenStatus = http.StatusGatewayTimeout }, want: core.ErrOIDCProviderUnavailable},
		{name: "malformed token response", mutate: func(f *fakeProvider) { f.tokenBody = `{` }, opaque: true},
		{name: "oversized token response", mutate: func(f *fakeProvider) { f.tokenLarge = true }, opaque: true},
		{name: "jwks 401", mutate: func(f *fakeProvider) { f.jwksStatus = http.StatusUnauthorized }, opaque: true},
		{name: "jwks 404", mutate: func(f *fakeProvider) { f.jwksStatus = http.StatusNotFound }, opaque: true},
		{name: "malformed jwks", mutate: func(f *fakeProvider) { f.jwksBody = `{` }, opaque: true},
		{name: "jwks 503", mutate: func(f *fakeProvider) { f.jwksStatus = http.StatusServiceUnavailable }, want: core.ErrOIDCProviderUnavailable},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newFakeProvider(t)
			testCase.mutate(fake)
			_, err := newAdapter(t, fake).Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce")
			if testCase.opaque && (err == nil || errors.Is(err, core.ErrOIDCRejected) || errors.Is(err, core.ErrOIDCProviderUnavailable)) {
				t.Fatalf("Exchange error = %v, want opaque internal failure", err)
			}
			if !testCase.opaque && !errors.Is(err, testCase.want) {
				t.Fatalf("Exchange error = %v, want %v", err, testCase.want)
			}
			if fake.tokenHits != 1 {
				t.Fatalf("failed token POSTs = %d, want exactly 1", fake.tokenHits)
			}
		})
	}
}

func TestProviderRejectsTokenRedirectWithoutContactingTarget(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHits.Add(1)
	}))
	t.Cleanup(target.Close)
	fake := newFakeProvider(t)
	fake.tokenRedirect = target.URL
	_, err := newAdapter(t, fake).Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce")
	if err == nil || errors.Is(err, core.ErrOIDCRejected) || errors.Is(err, core.ErrOIDCProviderUnavailable) {
		t.Fatalf("Exchange redirect error = %v, want opaque configuration failure", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("token redirect target hits = %d, want 0", targetHits.Load())
	}
}

func TestProviderRejectsJWKSRedirectWithoutContactingTarget(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHits.Add(1)
	}))
	t.Cleanup(target.Close)
	fake := newFakeProvider(t)
	fake.jwksRedirect = target.URL
	_, err := newAdapter(t, fake).Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce")
	if err == nil || errors.Is(err, core.ErrOIDCRejected) || errors.Is(err, core.ErrOIDCProviderUnavailable) {
		t.Fatalf("Exchange redirect error = %v, want opaque configuration failure", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("JWKS redirect target hits = %d, want 0", targetHits.Load())
	}
}

func TestProviderJWKSCallerCancellation(t *testing.T) {
	fake := newFakeProvider(t)
	fake.jwksStarted = make(chan struct{}, 1)
	fake.jwksCanceled = make(chan struct{})
	cfg := testConfig(fake.server.URL, time.Hour)
	cfg.RoleClaim = "groups"
	provider, err := adapter.New(context.Background(), cfg, adapter.Dependencies{
		Clock: testutil.NewFakeClock(oidcFixtureNow),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cleanupAdapter(t, provider)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, exchangeErr := provider.Exchange(ctx, "code", strings.Repeat("v", 43), "expected-nonce")
		done <- exchangeErr
	}()
	<-fake.jwksStarted
	cancel()
	<-fake.jwksCanceled
	if err := <-done; err == nil ||
		errors.Is(err, core.ErrOIDCProviderUnavailable) || errors.Is(err, core.ErrOIDCRejected) {
		t.Fatalf("cancelled exchange error = %v, want opaque cancellation", err)
	}
}

func TestProviderJWKSCacheRefetchesOnceForUnknownKey(t *testing.T) {
	fake := newFakeProvider(t)
	provider := newAdapter(t, fake)
	if _, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce"); err != nil {
		t.Fatalf("warm JWKS cache: %v", err)
	}
	fake.mu.Lock()
	fake.kid = "unknown-key"
	before := fake.jwksHits
	fake.mu.Unlock()
	if _, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce"); err == nil {
		t.Fatal("unknown key token unexpectedly verified")
	}
	fake.mu.Lock()
	hits := fake.jwksHits - before
	fake.mu.Unlock()
	if hits != 1 {
		t.Fatalf("unknown kid JWKS refetches = %d, want 1", hits)
	}
}

func TestProviderJWKSCacheDropsRevokedKeyAfterExpiry(t *testing.T) {
	fake := newFakeProvider(t)
	clock := testutil.NewFakeClock(oidcFixtureNow)
	provider, err := adapter.New(context.Background(), testConfig(fake.server.URL, time.Second), adapter.Dependencies{Clock: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cleanupAdapter(t, provider)
	if _, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce"); err != nil {
		t.Fatalf("warm JWKS cache: %v", err)
	}
	fake.mu.Lock()
	fake.jwksBody = jwkDocument(t, jwk("replacement", &fake.badKey.PublicKey))
	before := fake.jwksHits
	fake.mu.Unlock()
	clock.Advance(6 * time.Minute)
	if _, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce"); !errors.Is(err, core.ErrOIDCRejected) {
		t.Fatalf("revoked key Exchange error = %v, want terminal rejection", err)
	}
	fake.mu.Lock()
	hits := fake.jwksHits - before
	fake.mu.Unlock()
	if hits != 1 {
		t.Fatalf("expired JWKS fetches = %d, want 1", hits)
	}
}

func TestProviderJWKSConcurrentMissesCollapse(t *testing.T) {
	const callers = 8
	fake := newFakeProvider(t)
	fake.tokenStarted = make(chan struct{}, callers)
	fake.jwksStarted = make(chan struct{}, callers)
	fake.jwksRelease = make(chan struct{})
	provider := newAdapter(t, fake)
	errorsCh := make(chan error, callers)
	for range callers {
		go func() {
			_, err := provider.Exchange(context.Background(), "code", strings.Repeat("v", 43), "expected-nonce")
			errorsCh <- err
		}()
	}
	for range callers {
		<-fake.tokenStarted
	}
	<-fake.jwksStarted
	close(fake.jwksRelease)
	for range callers {
		if err := <-errorsCh; err != nil {
			t.Fatalf("Exchange: %v", err)
		}
	}
	fake.mu.Lock()
	hits := fake.jwksHits
	fake.mu.Unlock()
	if hits != 1 {
		t.Fatalf("concurrent JWKS fetches = %d, want 1", hits)
	}
}

func ExampleProvider() {
	fmt.Println("generic OIDC adapter")
	// Output: generic OIDC adapter
}
