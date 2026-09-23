// Package oidc adapts generic OpenID Connect providers to Bloom's core seam.
// Protocol dependencies are confined to this package.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxReadBodyBytes  = 1 << 20
	maxIssuerBytes    = core.MaxOIDCIssuerBytes
	maxSubjectBytes   = core.MaxOIDCSubjectBytes
	maxUsernameBytes  = 1024
	maxRoleValues     = 64
	maxRoleValueBytes = 256
	maxRetryDelay     = 5 * time.Second
)

var errResponseBodyTooLarge = errors.New("OpenID Connect response body exceeds limit")

// Provider is one discovered and cached generic OpenID Connect provider.
type Provider struct {
	oauth         oauth2.Config
	verifier      *coreoidc.IDTokenVerifier
	keySet        *remoteKeySet
	tokenClient   *http.Client
	transports    []*http.Transport
	tokenTimeout  time.Duration
	jwksTimeout   time.Duration
	clientID      string
	usernameClaim string
	roleClaim     string
}

// Metrics records bounded OIDC dependency operation outcomes.
type Metrics interface {
	ObserveOIDCDependency(operation, outcome string, seconds float64)
}

// Clock supplies the ID-token validation instant.
type Clock interface{ Now() time.Time }

// Dependencies are process-owned adapter dependencies.
type Dependencies struct {
	Metrics Metrics
	Clock   Clock
	Random  func(time.Duration) (time.Duration, error)
	Wait    func(context.Context, time.Duration) error
}

type nopMetrics struct{}

func (nopMetrics) ObserveOIDCDependency(string, string, float64) {}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

var _ core.OIDCProvider = (*Provider)(nil)

// New performs provider discovery at startup and builds a cached verifier.
func New(ctx context.Context, cfg config.OIDCConfig, supplied ...Dependencies) (*Provider, error) {
	deps := adapterDependencies(supplied)
	discovery := newOutboundClient(cfg.DiscoveryTimeout, true, "discovery", deps)
	defer discovery.close()
	discoveryCtx, cancel := context.WithTimeout(ctx, cfg.DiscoveryTimeout)
	defer cancel()
	discoveryCtx = coreoidc.ClientContext(discoveryCtx, discovery.client)
	provider, err := coreoidc.NewProvider(discoveryCtx, cfg.IssuerURL)
	if err != nil {
		return nil, dependencyError("discovery", err)
	}
	metadata, err := validateProviderEndpoints(provider, cfg.AllowInsecureIssuer)
	if err != nil {
		return nil, err
	}
	algorithms := supportedSigningAlgorithms(metadata.SigningAlgorithms)
	if len(algorithms) == 0 {
		return nil, errors.New("OpenID Connect provider advertises no supported ID-token signing algorithm")
	}
	authStyle, err := tokenAuthenticationStyle(metadata.TokenAuthMethods)
	if err != nil {
		return nil, err
	}
	token := newOutboundClient(cfg.TokenExchangeTimeout, false, "token_exchange", deps)
	jwks := newOutboundClient(cfg.JWKSFetchTimeout, true, "jwks", deps)
	keySet := newRemoteKeySet(metadata.JWKSURL, jwks.client, deps.Clock)
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = authStyle
	return &Provider{
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: string(cfg.ClientSecret.Bytes()),
			Endpoint: endpoint, RedirectURL: cfg.RedirectURL, Scopes: cfg.Scopes,
		},
		verifier: coreoidc.NewVerifier(cfg.IssuerURL, keySet, &coreoidc.Config{
			ClientID: cfg.ClientID, SupportedSigningAlgs: algorithms,
			Now: deps.Clock.Now,
		}),
		keySet:        keySet,
		tokenClient:   token.client,
		transports:    []*http.Transport{token.transport, jwks.transport},
		tokenTimeout:  cfg.TokenExchangeTimeout,
		jwksTimeout:   cfg.JWKSFetchTimeout,
		clientID:      cfg.ClientID,
		usernameClaim: cfg.UsernameClaim,
		roleClaim:     cfg.RoleClaim,
	}, nil
}

func adapterDependencies(supplied []Dependencies) Dependencies {
	deps := Dependencies{
		Metrics: nopMetrics{}, Clock: systemClock{},
		Random: randomRetryDelay, Wait: waitRetry,
	}
	if len(supplied) == 0 {
		return deps
	}
	if supplied[0].Metrics != nil {
		deps.Metrics = supplied[0].Metrics
	}
	if supplied[0].Clock != nil {
		deps.Clock = supplied[0].Clock
	}
	if supplied[0].Random != nil {
		deps.Random = supplied[0].Random
	}
	if supplied[0].Wait != nil {
		deps.Wait = supplied[0].Wait
	}
	return deps
}

type providerMetadata struct {
	JWKSURL           string   `json:"jwks_uri"`
	SigningAlgorithms []string `json:"id_token_signing_alg_values_supported"`
	TokenAuthMethods  []string `json:"token_endpoint_auth_methods_supported"`
}

func tokenAuthenticationStyle(methods []string) (oauth2.AuthStyle, error) {
	if len(methods) == 0 || slices.Contains(methods, "client_secret_basic") {
		return oauth2.AuthStyleInHeader, nil
	}
	if slices.Contains(methods, "client_secret_post") {
		return oauth2.AuthStyleInParams, nil
	}
	return oauth2.AuthStyleInHeader, errors.New("OpenID Connect provider advertises no supported token endpoint authentication method")
}

func supportedSigningAlgorithms(advertised []string) []string {
	algorithms := make([]string, 0, len(advertised))
	for _, algorithm := range advertised {
		switch algorithm {
		case coreoidc.RS256, coreoidc.RS384, coreoidc.RS512,
			coreoidc.ES256, coreoidc.ES384, coreoidc.ES512,
			coreoidc.PS256, coreoidc.PS384, coreoidc.PS512, coreoidc.EdDSA:
			algorithms = append(algorithms, algorithm)
		}
	}
	return algorithms
}

func validateProviderEndpoints(provider *coreoidc.Provider, allowInsecure bool) (providerMetadata, error) {
	var metadata providerMetadata
	if err := provider.Claims(&metadata); err != nil {
		return providerMetadata{}, fmt.Errorf("decode OpenID Connect provider metadata: %w", err)
	}
	endpoint := provider.Endpoint()
	values := []struct {
		name, raw string
	}{
		{"authorization_endpoint", endpoint.AuthURL},
		{"token_endpoint", endpoint.TokenURL},
		{"jwks_uri", metadata.JWKSURL},
	}
	for _, value := range values {
		if err := validateProviderURL(value.raw, allowInsecure); err != nil {
			return providerMetadata{}, fmt.Errorf("validate OpenID Connect %s: %w", value.name, err)
		}
	}
	return metadata, nil
}

func validateProviderURL(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("endpoint must be an absolute URL without credentials or fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if allowInsecure && parsed.Scheme == "http" && loopbackHost(parsed.Hostname()) {
		return nil
	}
	return errors.New("endpoint must use HTTPS; HTTP is allowed only on loopback in development")
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AuthorizationURL constructs the authorization-code request with nonce and
// PKCE S256 protection.
func (p *Provider) AuthorizationURL(state, nonce, codeChallenge string) string {
	return p.oauth.AuthCodeURL(state,
		coreoidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// Exchange exchanges one authorization code and verifies its ID token.
func (p *Provider) Exchange(ctx context.Context, code, verifier, nonce string) (core.OIDCClaims, error) {
	exchangeCtx, cancel := context.WithTimeout(ctx, p.tokenTimeout)
	defer cancel()
	exchangeCtx = context.WithValue(exchangeCtx, oauth2.HTTPClient, p.tokenClient)
	token, err := p.oauth.Exchange(exchangeCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return core.OIDCClaims{}, classifyExchangeError(ctx, err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return core.OIDCClaims{}, rejection("token response", errors.New("id_token omitted"))
	}
	idToken, err := p.verifyIDToken(ctx, rawIDToken)
	if err != nil {
		return core.OIDCClaims{}, err
	}
	if identityErr := validateIdentityClaims(idToken); identityErr != nil {
		return core.OIDCClaims{}, rejection("identity", identityErr)
	}
	if idToken.Nonce != nonce {
		return core.OIDCClaims{}, rejection("nonce", errors.New("nonce mismatch"))
	}
	claims, err := p.claims(idToken)
	if err != nil {
		return core.OIDCClaims{}, rejection("claims", err)
	}
	return claims, nil
}

func validateIdentityClaims(token *coreoidc.IDToken) error {
	if token == nil || token.Issuer == "" || len(token.Issuer) > maxIssuerBytes || !utf8.ValidString(token.Issuer) {
		return errors.New("verified id_token issuer is invalid")
	}
	if token.Subject == "" || len(token.Subject) > maxSubjectBytes || !utf8.ValidString(token.Subject) {
		return errors.New("verified id_token subject is invalid")
	}
	return nil
}

func (p *Provider) verifyIDToken(ctx context.Context, raw string) (*coreoidc.IDToken, error) {
	verifyCtx, cancel := context.WithTimeout(ctx, p.jwksTimeout)
	defer cancel()
	status := &verificationStatus{}
	verifyCtx = context.WithValue(verifyCtx, verificationStatusKey{}, status)
	token, err := p.verifier.Verify(verifyCtx, raw)
	if err == nil {
		return token, nil
	}
	return nil, classifyVerificationError(ctx, verifyCtx, status, err)
}

func classifyVerificationError(ctx, verifyCtx context.Context, status *verificationStatus, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return fmt.Errorf("OpenID Connect signing-key verification cancelled: %w", err)
	}
	if status.unavailable.Load() || errors.Is(verifyCtx.Err(), context.DeadlineExceeded) {
		return dependencyError("jwks", err)
	}
	if status.internal.Load() {
		return fmt.Errorf("OpenID Connect signing-key verification failed: %w", err)
	}
	return rejection("token", err)
}

func (p *Provider) claims(token *coreoidc.IDToken) (core.OIDCClaims, error) {
	claims := make(map[string]json.RawMessage)
	if err := token.Claims(&claims); err != nil {
		return core.OIDCClaims{}, fmt.Errorf("decode verified id_token claims: %w", err)
	}
	username, err := stringClaim(claims, p.usernameClaim)
	if err != nil {
		return core.OIDCClaims{}, err
	}
	roles, err := roleValues(claims, p.roleClaim)
	if err != nil {
		return core.OIDCClaims{}, err
	}
	if err := validateAuthorizedParty(claims, token.Audience, p.clientID); err != nil {
		return core.OIDCClaims{}, err
	}
	return core.OIDCClaims{Issuer: token.Issuer, Subject: token.Subject, Username: username, RoleValues: roles}, nil
}

func stringClaim(claims map[string]json.RawMessage, name string) (string, error) {
	var value string
	raw, ok := claims[name]
	if !ok || json.Unmarshal(raw, &value) != nil || value == "" || len(value) > maxUsernameBytes || !utf8.ValidString(value) {
		return "", fmt.Errorf("verified id_token claim %q must be a non-empty string", name)
	}
	return value, nil
}

func roleValues(claims map[string]json.RawMessage, name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	raw, ok := claims[name]
	if !ok {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return validateRoleValues(name, values)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("verified id_token claim %q must be a string or string array", name)
	}
	return validateRoleValues(name, []string{value})
}

func validateRoleValues(name string, values []string) ([]string, error) {
	if len(values) > maxRoleValues {
		return nil, fmt.Errorf("verified id_token claim %q exceeds %d roles", name, maxRoleValues)
	}
	for _, value := range values {
		if value == "" || len(value) > maxRoleValueBytes || !utf8.ValidString(value) {
			return nil, fmt.Errorf("verified id_token claim %q contains an invalid role value", name)
		}
	}
	return values, nil
}

func validateAuthorizedParty(claims map[string]json.RawMessage, audience []string, clientID string) error {
	raw, present := claims["azp"]
	if !present && len(audience) > 1 {
		return errors.New("verified id_token with multiple audiences must contain azp")
	}
	if !present {
		return nil
	}
	var authorizedParty string
	if json.Unmarshal(raw, &authorizedParty) != nil || authorizedParty == "" || authorizedParty != clientID {
		return errors.New("verified id_token azp does not identify this client")
	}
	return nil
}

func rejection(stage string, err error) error {
	return &core.OIDCRejection{Stage: stage, Err: err}
}

func dependencyError(operation string, err error) error {
	return &core.OIDCDependencyError{Operation: operation, Err: err}
}

func classifyExchangeError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return fmt.Errorf("OpenID Connect token exchange cancelled: %w", err)
	}
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) && retrieve.Response != nil {
		status := retrieve.Response.StatusCode
		if retrieve.ErrorCode == "invalid_grant" {
			return rejection("token exchange", err)
		}
		if retryableStatus(status) {
			return dependencyError("token_exchange", err)
		}
		return fmt.Errorf("OpenID Connect token exchange configuration failure: %w", err)
	}
	if redirectErr, ok := errors.AsType[*redirectPolicyError](err); ok && redirectErr != nil {
		return fmt.Errorf("OpenID Connect token endpoint redirect rejected: %w", err)
	}
	if retryableNetworkError(err) {
		return dependencyError("token_exchange", err)
	}
	return fmt.Errorf("OpenID Connect token exchange failed: %w", err)
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

type outboundClient struct {
	client    *http.Client
	transport *http.Transport
}

func newOutboundClient(timeout time.Duration, retryGET bool, operation string, deps Dependencies) outboundClient {
	transport := &http.Transport{
		MaxIdleConns: 20, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
	}
	var roundTripper http.RoundTripper = otelhttp.NewTransport(transport)
	roundTripper = observedTransport{base: roundTripper, operation: operation, metrics: deps.Metrics, clock: deps.Clock}
	if retryGET {
		roundTripper = retryTransport{
			base: roundTripper, operation: operation, metrics: deps.Metrics,
			policy: retryPolicy{clock: deps.Clock, random: deps.Random, wait: deps.Wait},
		}
	}
	roundTripper = boundedTransport{base: roundTripper}
	roundTripper = deadlineTransport{base: roundTripper, timeout: timeout}
	return outboundClient{
		client:    &http.Client{Transport: roundTripper, Timeout: timeout, CheckRedirect: rejectRedirect},
		transport: transport,
	}
}

func (c outboundClient) close() { c.transport.CloseIdleConnections() }

// Close releases the provider's long-lived token and signing-key transports.
func (p *Provider) Close(ctx context.Context) error {
	err := p.keySet.close(ctx)
	for _, transport := range p.transports {
		transport.CloseIdleConnections()
	}
	return err
}

type redirectPolicyError struct{}

func (*redirectPolicyError) Error() string { return "OpenID Connect redirects are disabled" }

func rejectRedirect(*http.Request, []*http.Request) error { return &redirectPolicyError{} }

type deadlineTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t deadlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(request.Context(), t.timeout)
	response, err := t.base.RoundTrip(request.Clone(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

type observedTransport struct {
	base      http.RoundTripper
	operation string
	metrics   Metrics
	clock     Clock
}

func (t observedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	started := t.clock.Now()
	response, err := t.base.RoundTrip(request)
	outcome := "request_success"
	if err != nil {
		outcome = "request_failure"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(request.Context().Err(), context.DeadlineExceeded) {
			outcome = "timeout"
		}
	} else if response.StatusCode >= http.StatusBadRequest {
		outcome = "request_failure"
	}
	elapsed := max(time.Duration(0), t.clock.Now().Sub(started))
	t.metrics.ObserveOIDCDependency(t.operation, outcome, elapsed.Seconds())
	return response, err
}

type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &boundedBody{body: response.Body, remaining: maxReadBodyBytes}
	return response, nil
}

type boundedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(buffer []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		count, err := b.body.Read(probe[:])
		if count > 0 {
			return 0, errResponseBodyTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > b.remaining {
		buffer = buffer[:b.remaining]
	}
	count, err := b.body.Read(buffer)
	b.remaining -= int64(count)
	return count, err
}

func (b *boundedBody) Close() error { return b.body.Close() }

type retryTransport struct {
	base      http.RoundTripper
	operation string
	metrics   Metrics
	policy    retryPolicy
}

func (t retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	const attempts = 3
	var lastErr error
	for attempt := range attempts {
		response, err := t.base.RoundTrip(request)
		if !retryable(request, response, err) {
			return response, err
		}
		lastErr = err
		if response != nil {
			lastErr = drainRetryResponse(response)
		}
		if attempt+1 < attempts {
			delay, err := t.policy.delay(request.Context(), response, attempt+1)
			if err != nil {
				return nil, err
			}
			t.metrics.ObserveOIDCDependency(t.operation, "retry", 0)
			if err := t.policy.wait(request.Context(), delay); err != nil {
				return nil, err
			}
		}
	}
	t.metrics.ObserveOIDCDependency(t.operation, "exhausted", 0)
	return nil, dependencyError(t.operation, lastErr)
}

func drainRetryResponse(response *http.Response) error {
	err := fmt.Errorf("upstream returned %s", response.Status)
	if _, drainErr := io.CopyN(io.Discard, response.Body, maxReadBodyBytes); drainErr != nil && !errors.Is(drainErr, io.EOF) {
		err = errors.Join(err, fmt.Errorf("drain retry response: %w", drainErr))
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close retry response: %w", closeErr))
	}
	return err
}

type retryPolicy struct {
	clock  Clock
	random func(time.Duration) (time.Duration, error)
	wait   func(context.Context, time.Duration) error
}

func (p retryPolicy) delay(ctx context.Context, response *http.Response, attempt int) (time.Duration, error) {
	if response != nil {
		if delay, ok := parseRetryAfter(response.Header.Get("Retry-After"), p.clock.Now()); ok {
			return p.capDelay(ctx, delay), nil
		}
	}
	capDelay := min(time.Duration(1<<attempt)*50*time.Millisecond, maxRetryDelay)
	delay, err := p.random(capDelay)
	if err != nil {
		return 0, err
	}
	if delay < 0 || delay > capDelay {
		return 0, errors.New("retry jitter is outside its requested bound")
	}
	return p.capDelay(ctx, delay), nil
}

func randomRetryDelay(limit time.Duration) (time.Duration, error) {
	random, err := rand.Int(rand.Reader, big.NewInt(int64(limit)+1))
	if err != nil {
		return 0, fmt.Errorf("generate retry jitter: %w", err)
	}
	return time.Duration(random.Int64()), nil
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseUint(value, 10, 31); err == nil {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(0, when.Sub(now)), true
}

func (p retryPolicy) capDelay(ctx context.Context, delay time.Duration) time.Duration {
	delay = min(delay, maxRetryDelay)
	if deadline, ok := ctx.Deadline(); ok {
		delay = min(delay, max(0, deadline.Sub(p.clock.Now())))
	}
	return delay
}

func retryable(request *http.Request, response *http.Response, err error) bool {
	if request.Method != http.MethodGet || request.Context().Err() != nil {
		return false
	}
	if err != nil {
		return retryableNetworkError(err)
	}
	return retryableStatus(response.StatusCode)
}

func retryableNetworkError(err error) bool {
	var redirectErr *redirectPolicyError
	var certificateErr *tls.CertificateVerificationError
	var dnsErr *net.DNSError
	var networkErr net.Error
	switch {
	case errors.As(err, &redirectErr), errors.As(err, &certificateErr):
		return false
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET):
		return true
	case errors.As(err, &dnsErr):
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	case errors.As(err, &networkErr):
		return networkErr.Timeout()
	default:
		return false
	}
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type verificationStatusKey struct{}

type verificationStatus struct {
	unavailable atomic.Bool
	internal    atomic.Bool
}
