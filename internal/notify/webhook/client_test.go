package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestSendSignsStructuredPayload(t *testing.T) {
	t.Parallel()
	const secret = "shared-secret"
	const deliveryID = "00000000-0000-4000-8000-000000000002"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-Bloom-Signature"); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		if got := r.Header.Get("X-Bloom-Event"); got != "approved" {
			t.Errorf("event = %q", got)
		}
		if got := r.Header.Get("X-Bloom-Delivery-ID"); got != deliveryID {
			t.Errorf("delivery header = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != deliveryID {
			t.Errorf("idempotency key = %q", got)
		}
		var payload core.NotificationPayload
		if err := json.Unmarshal(body, &payload); err != nil || payload.DeliveryID != deliveryID {
			t.Errorf("payload = %+v, error = %v", payload, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("default HTTP transport has unexpected type")
	}
	transport := base.Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client, err := New(Config{
		URL: "http://example.test:" + port, SharedSecret: secret, AllowInsecure: true, AllowPrivate: true,
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := core.NotificationPayload{
		DeliveryID: deliveryID,
		EventType:  core.RequestEventApproved, RequestID: "00000000-0000-4000-8000-000000000001",
		Title: "Example", Kind: core.MediaKindMovie, Status: core.RequestApproved,
		RequesterUsername: "requester", ActorUsername: "actor", OccurredAt: time.Now().UTC(),
	}
	if err := client.Send(t.Context(), core.RenderedNotification{Payload: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}
