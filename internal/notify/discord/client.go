// Package discord delivers Bloom events through Discord webhooks.
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/notify/httpx"
)

const (
	discordTitleRunes       = 256
	discordDescriptionRunes = 4096
	discordFieldNameRunes   = 256
	discordFieldValueRunes  = 1024
	discordEmbedRunes       = 6000
	discordPlaceholder      = "—"
)

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Fields      []discordField `json:"fields"`
	Timestamp   string         `json:"timestamp"`
}

type discordMessage struct {
	Embeds []discordEmbed `json:"embeds"`
}

// Config defines one Discord webhook destination.
type Config struct {
	WebhookURL    string
	AllowInsecure bool
	AllowPrivate  bool
	Timeout       time.Duration
	HTTPClient    *http.Client
}

// Client delivers Discord embed messages.
type Client struct{ http *httpx.Client }

var _ core.NotificationChannel = (*Client)(nil)

// New validates and constructs a Discord client.
func New(config Config) (*Client, error) {
	client, err := httpx.New(httpx.Config{
		URL: config.WebhookURL, AllowInsecure: config.AllowInsecure, AllowPrivate: config.AllowPrivate,
		Timeout: config.Timeout, HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("discord config: %w", err)
	}
	return &Client{http: client}, nil
}

// Kind reports the adapter kind.
func (c *Client) Kind() core.NotificationKind { return core.NotificationKindDiscord }

// Probe sends a marked test embed.
func (c *Client) Probe(ctx context.Context) error {
	payload := core.NotificationPayload{
		EventType: core.RequestEventCreated, RequestID: "00000000-0000-4000-8000-000000000000",
		Title: "Bloom test notification", Kind: core.MediaKindMovie, Status: core.RequestPending,
		RequesterUsername: "bloom", ActorUsername: "bloom", OccurredAt: time.Unix(0, 0).UTC(), Test: true,
	}
	return c.Send(ctx, core.RenderedNotification{Subject: "Bloom test", MarkdownBody: "Bloom test notification", Payload: payload})
}

// Send posts one Discord embed.
func (c *Client) Send(ctx context.Context, message core.RenderedNotification) error {
	body := discordMessage{Embeds: []discordEmbed{buildEmbed(message)}}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode Discord payload: %w", err)
	}
	return c.http.Post(ctx, encoded, nil)
}

func buildEmbed(message core.RenderedNotification) discordEmbed {
	embed := discordEmbed{
		Title: message.Subject, Description: message.MarkdownBody,
		Timestamp: message.Payload.OccurredAt.UTC().Format(time.RFC3339),
		Fields: []discordField{
			{Name: "Event", Value: string(message.Payload.EventType), Inline: true},
			{Name: "Kind", Value: string(message.Payload.Kind), Inline: true},
			{Name: "Requester", Value: message.Payload.RequesterUsername, Inline: true},
			{Name: "Actor", Value: message.Payload.ActorUsername, Inline: true},
			{Name: "Request ID", Value: message.Payload.RequestID},
			{Name: "Test", Value: strconv.FormatBool(message.Payload.Test), Inline: true},
		},
	}
	return sanitizeEmbed(embed)
}

func sanitizeEmbed(embed discordEmbed) discordEmbed {
	embed.Title = truncateRunes(escapeMarkdown(embed.Title), discordTitleRunes)
	embed.Description = truncateRunes(escapeMarkdown(embed.Description), discordDescriptionRunes)
	for index := range embed.Fields {
		embed.Fields[index].Name = escapeMarkdown(embed.Fields[index].Name)
		embed.Fields[index].Value = escapeMarkdown(embed.Fields[index].Value)
	}
	remaining := discordEmbedRunes - len([]rune(embed.Title)) - len([]rune(embed.Description))
	embed.Fields = fitDiscordFields(embed.Fields, remaining)
	return embed
}

func fitDiscordFields(fields []discordField, remaining int) []discordField {
	result := make([]discordField, 0, len(fields))
	for index, field := range fields {
		field.Name = truncateRunes(field.Name, discordFieldNameRunes)
		if field.Value == "" {
			field.Value = discordPlaceholder
		}
		reserve := discordFieldReserve(fields[index+1:])
		limit := min(discordFieldValueRunes, max(1, remaining-len([]rune(field.Name))-reserve))
		field.Value = truncateRunes(field.Value, limit)
		remaining -= len([]rune(field.Name)) + len([]rune(field.Value))
		result = append(result, field)
	}
	return result
}

func discordFieldReserve(fields []discordField) int {
	result := len(fields)
	for _, field := range fields {
		result += min(len([]rune(field.Name)), discordFieldNameRunes)
	}
	return result
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func escapeMarkdown(value string) string {
	const special = `\` + "`*_{}[]()#+-.!|>~"
	var result strings.Builder
	result.Grow(len(value))
	for _, character := range value {
		if strings.ContainsRune(special, character) {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
	}
	return result.String()
}

// CloseIdleConnections releases pooled HTTP connections.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }
