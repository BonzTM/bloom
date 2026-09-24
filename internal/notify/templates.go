package notify

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// TemplateData is the fixed field set available to operators.
type TemplateData struct {
	Title      string
	Kind       string
	Status     string
	Requester  string
	Actor      string
	Reason     string
	RequestID  string
	OccurredAt time.Time
}

var allowedTemplateFields = map[string]struct{}{
	"Title": {}, "Kind": {}, "Status": {}, "Requester": {}, "Actor": {},
	"Reason": {}, "RequestID": {}, "OccurredAt": {},
}

var defaultSubjects = map[core.RequestEventType]string{
	core.RequestEventCreated:    `Request created: {{.Title}}`,
	core.RequestEventApproved:   `Request approved: {{.Title}}`,
	core.RequestEventDeclined:   `Request declined: {{.Title}}`,
	core.RequestEventDispatched: `Request dispatched: {{.Title}}`,
	core.RequestEventAvailable:  `Request available: {{.Title}}`,
	core.RequestEventFailed:     `Request failed: {{.Title}}`,
}

var defaultBodies = map[core.RequestEventType]string{
	core.RequestEventCreated:    `{{.Requester}} requested {{.Title}} ({{.Kind}}).`,
	core.RequestEventApproved:   `{{.Actor}} approved {{.Requester}}'s request for {{.Title}}.`,
	core.RequestEventDeclined:   `{{.Actor}} declined {{.Requester}}'s request for {{.Title}}. {{.Reason}}`,
	core.RequestEventDispatched: `{{.Title}} was sent to the download manager.`,
	core.RequestEventAvailable:  `{{.Title}} is now available.`,
	core.RequestEventFailed:     `{{.Title}} could not be fulfilled. {{.Reason}}`,
}

// ValidateTemplates rejects functions, control flow, and unknown fields.
func ValidateTemplates(subject, body string) error {
	if len(subject) > core.MaxNotificationTemplateBytes || len(body) > core.MaxNotificationTemplateBytes {
		return core.ErrInvalidArgument
	}
	for _, value := range []string{subject, body} {
		if value == "" {
			continue
		}
		parsed, err := template.New("notification").Option("missingkey=error").Parse(value)
		if err != nil {
			return fmt.Errorf("parse notification template: %w", core.ErrInvalidArgument)
		}
		if err := validateTemplateNode(parsed.Root); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplateNode(node parse.Node) error {
	switch value := node.(type) {
	case *parse.ListNode:
		for _, child := range value.Nodes {
			if err := validateTemplateNode(child); err != nil {
				return err
			}
		}
		return nil
	case *parse.TextNode:
		return nil
	case *parse.ActionNode:
		if len(value.Pipe.Decl) != 0 || len(value.Pipe.Cmds) != 1 || len(value.Pipe.Cmds[0].Args) != 1 {
			return core.ErrInvalidArgument
		}
		field, ok := value.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
		if !ok || len(field.Ident) != 1 {
			return core.ErrInvalidArgument
		}
		if _, ok := allowedTemplateFields[field.Ident[0]]; !ok {
			return core.ErrInvalidArgument
		}
		return nil
	default:
		return core.ErrInvalidArgument
	}
}

// RenderMessage applies validated defaults or overrides to a payload.
func RenderMessage(payload core.NotificationPayload, settings core.NotificationSettings) (core.RenderedNotification, error) {
	if err := core.ValidateNotificationPayload(payload); err != nil {
		return core.RenderedNotification{}, err
	}
	subject, body := settings.SubjectTemplate, settings.BodyTemplate
	if subject == "" {
		subject = defaultSubjects[payload.EventType]
	}
	if body == "" {
		body = defaultBodies[payload.EventType]
	}
	if subject == "" || body == "" {
		return core.RenderedNotification{}, core.ErrInvalidArgument
	}
	if err := ValidateTemplates(subject, body); err != nil {
		return core.RenderedNotification{}, err
	}
	data := TemplateData{
		Title: payload.Title, Kind: string(payload.Kind), Status: string(payload.Status),
		Requester: payload.RequesterUsername, Actor: payload.ActorUsername, Reason: payload.Reason,
		RequestID: payload.RequestID, OccurredAt: payload.OccurredAt,
	}
	renderedSubject, err := executeTemplate(subject, data)
	if err != nil {
		return core.RenderedNotification{}, err
	}
	renderedBody, err := executeTemplate(body, data)
	if err != nil {
		return core.RenderedNotification{}, err
	}
	return core.RenderedNotification{
		Subject: renderedSubject, PlainBody: renderedBody, MarkdownBody: renderedBody, Payload: payload,
	}, nil
}

func executeTemplate(source string, data TemplateData) (string, error) {
	parsed, err := template.New("notification").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse notification template: %w", err)
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, data); err != nil {
		return "", fmt.Errorf("render notification template: %w", err)
	}
	if output.Len() > core.MaxNotificationTemplateBytes {
		return "", errors.New("render notification template: output exceeds limit")
	}
	return strings.TrimSpace(output.String()), nil
}
