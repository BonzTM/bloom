// Package webhook delivers signed structured Bloom events.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/notify/httpx"
)

// Config defines a signed generic webhook destination.
type Config struct {
	URL           string
	SharedSecret  string
	AllowInsecure bool
	AllowPrivate  bool
	Timeout       time.Duration
	HTTPClient    *http.Client
}

// Client delivers signed structured webhook events.
type Client struct {
	http   *httpx.Client
	secret []byte
}

var _ core.NotificationChannel = (*Client)(nil)

// New validates and constructs a webhook client.
func New(config Config) (*Client, error) {
	if config.SharedSecret == "" {
		return nil, core.ErrInvalidArgument
	}
	client, err := httpx.New(httpx.Config{
		URL: config.URL, AllowInsecure: config.AllowInsecure, AllowPrivate: config.AllowPrivate,
		Timeout: config.Timeout, HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("webhook config: %w", err)
	}
	return &Client{http: client, secret: []byte(config.SharedSecret)}, nil
}

// Kind reports the adapter kind.
func (c *Client) Kind() core.NotificationKind { return core.NotificationKindWebhook }

// Probe sends a marked test payload.
func (c *Client) Probe(ctx context.Context) error {
	deliveryID, err := core.NewID()
	if err != nil {
		return fmt.Errorf("create webhook probe delivery id: %w", err)
	}
	return c.Send(ctx, core.RenderedNotification{Payload: core.NotificationPayload{
		DeliveryID: deliveryID, EventType: core.RequestEventCreated,
		RequestID: "00000000-0000-4000-8000-000000000000",
		Title:     "Bloom test notification", Kind: core.MediaKindMovie, Status: core.RequestPending,
		RequesterUsername: "bloom", ActorUsername: "bloom", OccurredAt: time.Unix(0, 0).UTC(), Test: true,
	}})
}

// Send signs and posts one structured payload.
func (c *Client) Send(ctx context.Context, message core.RenderedNotification) error {
	if !core.ValidID(message.Payload.DeliveryID) {
		return core.ErrInvalidArgument
	}
	body, err := json.Marshal(message.Payload)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	mac := hmac.New(sha256.New, c.secret)
	if _, err := mac.Write(body); err != nil {
		return fmt.Errorf("sign webhook payload: %w", err)
	}
	headers := map[string]string{
		"X-Bloom-Signature":   "sha256=" + hex.EncodeToString(mac.Sum(nil)),
		"X-Bloom-Event":       string(message.Payload.EventType),
		"X-Bloom-Delivery-ID": message.Payload.DeliveryID,
		"Idempotency-Key":     message.Payload.DeliveryID,
	}
	return c.http.Post(ctx, body, headers)
}

// CloseIdleConnections releases pooled HTTP connections.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }
