package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxJWKCount     = 128
	maxJWKSCacheTTL = 5 * time.Minute
)

type keyLoad struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	keys    []jose.JSONWebKey
	err     error
}

type remoteKeySet struct {
	url         string
	client      *http.Client
	clock       Clock
	mu          sync.RWMutex
	keys        []jose.JSONWebKey
	expiresAt   time.Time
	load        *keyLoad
	waiterAdded func()
}

func newRemoteKeySet(url string, client *http.Client, clock Clock) *remoteKeySet {
	return &remoteKeySet{url: url, client: client, clock: clock}
}

func (s *remoteKeySet) VerifySignature(ctx context.Context, token string) ([]byte, error) {
	signature, keyID, err := parseTokenSignature(token)
	if err != nil {
		return nil, err
	}
	if payload, ok := verifyWithKeys(signature, keyID, s.cachedKeys()); ok {
		return payload, nil
	}
	keys, err := s.refresh(ctx)
	if err != nil {
		markVerificationFailure(ctx, err)
		return nil, fmt.Errorf("fetch signing keys: %w", err)
	}
	if payload, ok := verifyWithKeys(signature, keyID, keys); ok {
		return payload, nil
	}
	return nil, errors.New("no OpenID Connect signing key verified the token")
}

func parseTokenSignature(token string) (*jose.JSONWebSignature, string, error) {
	signature, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{
		jose.RS256, jose.RS384, jose.RS512,
		jose.ES256, jose.ES384, jose.ES512,
		jose.PS256, jose.PS384, jose.PS512, jose.EdDSA,
	})
	if err != nil {
		return nil, "", fmt.Errorf("parse signed token: %w", err)
	}
	if len(signature.Signatures) != 1 {
		return nil, "", errors.New("signed token must contain exactly one signature")
	}
	return signature, signature.Signatures[0].Header.KeyID, nil
}

func verifyWithKeys(signature *jose.JSONWebSignature, keyID string, keys []jose.JSONWebKey) ([]byte, bool) {
	algorithm := signature.Signatures[0].Header.Algorithm
	for _, key := range keys {
		if (keyID != "" && key.KeyID != keyID) || (key.Algorithm != "" && key.Algorithm != algorithm) {
			continue
		}
		payload, err := signature.Verify(&key)
		if err == nil {
			return payload, true
		}
	}
	return nil, false
}

func (s *remoteKeySet) cachedKeys() []jose.JSONWebKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.clock.Now().Before(s.expiresAt) {
		return nil
	}
	return slices.Clone(s.keys)
}

func (s *remoteKeySet) refresh(ctx context.Context) ([]jose.JSONWebKey, error) {
	s.mu.Lock()
	load := s.load
	if load == nil {
		fetchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		load = &keyLoad{done: make(chan struct{}), cancel: cancel}
		s.load = load
		go s.completeRefresh(fetchCtx, load)
	}
	load.waiters++
	s.mu.Unlock()
	if s.waiterAdded != nil {
		s.waiterAdded()
	}
	select {
	case <-load.done:
		return slices.Clone(load.keys), load.err
	case <-ctx.Done():
		s.cancelRefreshWaiter(load)
		return nil, ctx.Err()
	}
}

func (s *remoteKeySet) completeRefresh(ctx context.Context, load *keyLoad) {
	keys, err := s.fetch(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	load.keys, load.err = slices.Clone(keys), err
	if err == nil {
		s.keys = slices.Clone(keys)
		s.expiresAt = s.clock.Now().Add(maxJWKSCacheTTL)
	}
	if s.load == load {
		s.load = nil
	}
	load.cancel()
	close(load.done)
}

func (s *remoteKeySet) cancelRefreshWaiter(load *keyLoad) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.load != load {
		return
	}
	load.waiters--
	if load.waiters == 0 {
		load.cancel()
	}
}

func (s *remoteKeySet) close(ctx context.Context) error {
	s.mu.Lock()
	load := s.load
	if load != nil {
		load.cancel()
	}
	s.mu.Unlock()
	if load == nil {
		return ctx.Err()
	}
	select {
	case <-load.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *remoteKeySet) fetch(ctx context.Context) ([]jose.JSONWebKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build signing-key request: %w", err)
	}
	response, err := s.client.Do(request)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("request signing keys: %w", err)
	}
	keys, decodeErr := decodeJWKResponse(response)
	if decodeErr != nil && !transientJWKSResponseError(decodeErr) {
		decodeErr = &permanentJWKSResponseError{err: decodeErr}
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		decodeErr = errors.Join(decodeErr, fmt.Errorf("close signing-key response: %w", closeErr))
	}
	return keys, decodeErr
}

func decodeJWKResponse(response *http.Response) ([]jose.JSONWebKey, error) {
	if response.StatusCode != http.StatusOK {
		return nil, &jwksStatusError{status: response.Status}
	}
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode signing keys: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, err
	}
	return parseJWKs(document.Keys)
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("signing-key response contains trailing JSON")
		}
		return fmt.Errorf("decode signing-key response trailer: %w", err)
	}
	return nil
}

func parseJWKs(rawKeys []json.RawMessage) ([]jose.JSONWebKey, error) {
	if len(rawKeys) == 0 || len(rawKeys) > maxJWKCount {
		return nil, fmt.Errorf("signing-key response must contain 1-%d keys", maxJWKCount)
	}
	keys := make([]jose.JSONWebKey, 0, len(rawKeys))
	for _, raw := range rawKeys {
		var key jose.JSONWebKey
		if err := json.Unmarshal(raw, &key); err != nil {
			if errors.Is(err, jose.ErrUnsupportedKeyType) {
				continue
			}
			return nil, fmt.Errorf("decode signing key: %w", err)
		}
		if key.IsPublic() && (key.Use == "" || key.Use == "sig") && supportedJWKAlgorithm(key.Algorithm) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("signing-key response contains no supported public keys")
	}
	return keys, nil
}

func supportedJWKAlgorithm(algorithm string) bool {
	switch jose.SignatureAlgorithm(algorithm) {
	case "", jose.RS256, jose.RS384, jose.RS512,
		jose.ES256, jose.ES384, jose.ES512,
		jose.PS256, jose.PS384, jose.PS512, jose.EdDSA:
		return true
	default:
		return false
	}
}

type jwksStatusError struct{ status string }

func (e *jwksStatusError) Error() string { return "signing-key endpoint returned " + e.status }

type permanentJWKSResponseError struct{ err error }

func (e *permanentJWKSResponseError) Error() string { return e.err.Error() }
func (e *permanentJWKSResponseError) Unwrap() error { return e.err }

func markVerificationFailure(ctx context.Context, err error) {
	status, ok := ctx.Value(verificationStatusKey{}).(*verificationStatus)
	if !ok {
		return
	}
	if transientJWKSError(err) {
		status.unavailable.Store(true)
		return
	}
	status.internal.Store(true)
}

func transientJWKSError(err error) bool {
	if permanent, ok := errors.AsType[*permanentJWKSResponseError](err); ok && permanent != nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, core.ErrOIDCProviderUnavailable) ||
		retryableNetworkError(err)
}

func transientJWKSResponseError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) ||
		retryableNetworkError(err)
}
