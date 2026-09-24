package discord

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
)

func TestSendUsesOneEmbed(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var value struct {
			Embeds []struct {
				Title, Description string
				Fields             []json.RawMessage
			} `json:"embeds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			t.Error(err)
		}
		if len(value.Embeds) != 1 || value.Embeds[0].Title != "subject" || len(value.Embeds[0].Fields) != 6 {
			t.Errorf("payload = %+v", value)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	payload := core.NotificationPayload{
		EventType: core.RequestEventAvailable, RequestID: "00000000-0000-4000-8000-000000000001",
		Title: "Example", Kind: core.MediaKindMovie, Status: core.RequestAvailable,
		RequesterUsername: "requester", ActorUsername: "system", OccurredAt: time.Now().UTC(),
	}
	message := core.RenderedNotification{Subject: "subject", MarkdownBody: "description", Payload: payload}
	if err := client.Send(t.Context(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSendEnforcesDiscordEmbedLimits(t *testing.T) {
	t.Parallel()
	values := make(chan discordMessage, 1)
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var value discordMessage
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			t.Error(err)
		}
		values <- value
		w.WriteHeader(http.StatusNoContent)
	})
	payload := core.NotificationPayload{
		EventType: core.RequestEventAvailable, RequestID: "00000000-0000-4000-8000-000000000001",
		Title: "Example", Kind: core.MediaKindMovie, Status: core.RequestAvailable,
		RequesterUsername: strings.Repeat("r", core.MaxNotificationTemplateBytes),
		ActorUsername:     strings.Repeat("a", core.MaxNotificationTemplateBytes), OccurredAt: time.Now().UTC(),
	}
	message := core.RenderedNotification{
		Subject:      strings.Repeat("é", core.MaxNotificationTemplateBytes/2),
		MarkdownBody: strings.Repeat("b", core.MaxNotificationTemplateBytes), Payload: payload,
	}
	if err := client.Send(t.Context(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	value := <-values
	if len(value.Embeds) != 1 {
		t.Fatalf("embeds = %d", len(value.Embeds))
	}
	assertEmbedLimits(t, value.Embeds[0])
}

func TestBuildEmbedEscapesExternallyDerivedFieldValues(t *testing.T) {
	t.Parallel()
	message := core.RenderedNotification{
		Subject: "subject_with_markup", MarkdownBody: "description *with* markup",
		Payload: core.NotificationPayload{
			EventType: core.RequestEventCreated, RequestID: "00000000-0000-4000-8000-000000000001",
			Kind: core.MediaKindMovie, RequesterUsername: "a_b_c", ActorUsername: "*x*",
			OccurredAt: time.Unix(0, 0).UTC(),
		},
	}
	embed := buildEmbed(message)
	if embed.Title != `subject\_with\_markup` || embed.Description != `description \*with\* markup` {
		t.Fatalf("embed text = %q / %q", embed.Title, embed.Description)
	}
	if embed.Fields[2].Value != `a\_b\_c` || embed.Fields[3].Value != `\*x\*` {
		t.Fatalf("username fields = %q / %q", embed.Fields[2].Value, embed.Fields[3].Value)
	}
	sanitized := sanitizeEmbed(discordEmbed{Fields: []discordField{{Name: "field_name", Value: "field_value"}}})
	if sanitized.Fields[0].Name != `field\_name` || sanitized.Fields[0].Value != `field\_value` {
		t.Fatalf("custom field = %+v", sanitized.Fields[0])
	}
}

func assertEmbedLimits(t *testing.T, embed discordEmbed) {
	t.Helper()
	if !utf8.ValidString(embed.Title) || len([]rune(embed.Title)) != discordTitleRunes || !strings.HasSuffix(embed.Title, "…") {
		t.Errorf("title length = %d, valid = %v", len([]rune(embed.Title)), utf8.ValidString(embed.Title))
	}
	total := len([]rune(embed.Title)) + len([]rune(embed.Description))
	if len([]rune(embed.Description)) > discordDescriptionRunes {
		t.Errorf("description length = %d", len([]rune(embed.Description)))
	}
	for _, field := range embed.Fields {
		nameLength, valueLength := len([]rune(field.Name)), len([]rune(field.Value))
		if nameLength > discordFieldNameRunes || valueLength > discordFieldValueRunes {
			t.Errorf("field lengths = %d/%d", nameLength, valueLength)
		}
		total += nameLength + valueLength
	}
	if total > discordEmbedRunes {
		t.Errorf("embed text length = %d", total)
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
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
		WebhookURL: "http://example.test:" + port, AllowInsecure: true, AllowPrivate: true,
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
