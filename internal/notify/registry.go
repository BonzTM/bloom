package notify

import (
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/notify/discord"
	"github.com/BonzTM/bloom/internal/notify/email"
	"github.com/BonzTM/bloom/internal/notify/webhook"
)

// Credentials contains decrypted kind-specific adapter credentials.
type Credentials struct {
	WebhookURL   string `json:"webhook_url,omitempty"`
	SharedSecret string `json:"shared_secret,omitempty"`
	DiscordURL   string `json:"discord_url,omitempty"`
	Password     string `json:"password,omitempty"`
}

// Registry constructs adapters keyed by notification kind.
type Registry struct{}

// NewRegistry constructs an adapter registry.
func NewRegistry() *Registry { return &Registry{} }

// New constructs the adapter for one registration.
func (r *Registry) New(registration core.NotificationRegistration, credentials Credentials) (core.NotificationChannel, error) {
	settings := registration.Settings
	switch registration.Kind {
	case core.NotificationKindWebhook:
		return webhook.New(webhook.Config{
			URL: credentials.WebhookURL, SharedSecret: credentials.SharedSecret,
			AllowInsecure: settings.AllowInsecure, AllowPrivate: settings.AllowPrivate,
		})
	case core.NotificationKindDiscord:
		return discord.New(discord.Config{
			WebhookURL: credentials.DiscordURL, AllowInsecure: settings.AllowInsecure,
			AllowPrivate: settings.AllowPrivate,
		})
	case core.NotificationKindEmail:
		return email.New(email.Config{
			Host: registration.Target, Port: settings.SMTPPort, TLSMode: settings.TLSMode,
			AuthMode: settings.AuthMode, Username: settings.Username, Password: credentials.Password,
			From: settings.FromAddress, FromName: settings.FromName, Recipients: settings.Recipients,
			AllowPrivate: settings.AllowPrivate,
		})
	default:
		return nil, fmt.Errorf("notification kind: %w", core.ErrInvalidArgument)
	}
}
