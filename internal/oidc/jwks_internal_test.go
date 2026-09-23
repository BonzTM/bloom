package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRemoteKeySetCollapsesMissesAndKeepsWaitersCancellable(t *testing.T) {
	const callers = 4
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var hits atomic.Int32
	document := testJWKDocument(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		once.Do(func() { close(started) })
		<-release
		if _, err := w.Write(document); err != nil {
			return
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	set := newRemoteKeySet(server.URL, server.Client(), systemClock{})
	waiterAdded := make(chan struct{}, callers)
	set.waiterAdded = func() { waiterAdded <- struct{}{} }
	cancelCtx, cancel := context.WithCancel(context.Background())
	errorsCh := make(chan error, callers)
	go func() { _, err := set.refresh(cancelCtx); errorsCh <- err }()
	<-started
	for range callers - 1 {
		go func() { _, err := set.refresh(context.Background()); errorsCh <- err }()
	}
	for range callers {
		<-waiterAdded
	}
	cancel()
	if err := <-errorsCh; err == nil {
		t.Fatal("cancelled JWKS waiter succeeded")
	}
	close(release)
	for range callers - 1 {
		if err := <-errorsCh; err != nil {
			t.Fatalf("shared JWKS refresh: %v", err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("JWKS fetches = %d, want 1", hits.Load())
	}
}

func testJWKDocument(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	document := struct {
		Keys []any `json:"keys"`
	}{Keys: []any{map[string]any{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "key",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode JWK: %v", err)
	}
	return encoded
}
