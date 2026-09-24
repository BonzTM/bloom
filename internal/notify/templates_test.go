package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestValidateTemplatesRejectsUnknownFieldsAndActions(t *testing.T) {
	t.Parallel()
	for _, source := range []string{`{{.Secret}}`, `{{if .Title}}x{{end}}`, `{{printf "%s" .Title}}`} {
		if err := ValidateTemplates(source, ""); !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("ValidateTemplates(%q) = %v", source, err)
		}
	}
}

func TestRenderMessageUsesDefaultsAndPreservesPlainText(t *testing.T) {
	t.Parallel()
	payload := notificationTestPayload()
	payload.Title = "A *great* [movie]"
	message, err := RenderMessage(payload, core.NotificationSettings{})
	if err != nil {
		t.Fatalf("RenderMessage: %v", err)
	}
	if !strings.Contains(message.MarkdownBody, `A *great* [movie]`) {
		t.Fatalf("MarkdownBody = %q", message.MarkdownBody)
	}
	if message.MarkdownBody != message.PlainBody {
		t.Fatalf("rendered bodies differ: %q / %q", message.MarkdownBody, message.PlainBody)
	}
}

func TestRenderMessageUsesFixedFields(t *testing.T) {
	t.Parallel()
	payload := notificationTestPayload()
	settings := core.NotificationSettings{
		SubjectTemplate: `{{.Status}} {{.Title}}`,
		BodyTemplate:    `{{.Requester}}|{{.Actor}}|{{.Reason}}|{{.RequestID}}|{{.OccurredAt}}`,
	}
	message, err := RenderMessage(payload, settings)
	if err != nil {
		t.Fatalf("RenderMessage: %v", err)
	}
	if message.Subject != "pending Example" || !strings.Contains(message.PlainBody, "requester|actor|because|") {
		t.Fatalf("message = %+v", message)
	}
}

func notificationTestPayload() core.NotificationPayload {
	return core.NotificationPayload{
		EventType: core.RequestEventCreated, RequestID: "00000000-0000-4000-8000-000000000001",
		Title: "Example", Kind: core.MediaKindMovie, Status: core.RequestPending,
		RequesterUsername: "requester", ActorUsername: "actor", Reason: "because",
		OccurredAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
	}
}
