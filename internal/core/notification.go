package core

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Notification data bounds limit persisted, rendered, and transported values.
const (
	MaxNotificationNameBytes     = 100
	MaxNotificationTargetBytes   = 2048
	MaxNotificationSettingsBytes = 16 * 1024
	MaxNotificationPayloadBytes  = 32 * 1024
	MaxNotificationTemplateBytes = 4096
	MaxNotificationRecipients    = 32
	MaxNotificationAttempts      = 8
	MaxNotificationErrorBytes    = 512
)

// ErrNotificationChannelFailure identifies an opaque adapter failure at the HTTP boundary.
var ErrNotificationChannelFailure = errors.New("notification channel failure")

// ErrDuplicateNotificationRecipient identifies a repeated case-insensitive mailbox.
var ErrDuplicateNotificationRecipient = errors.New("duplicate notification recipient")

// NotificationKind identifies a registered adapter implementation.
type NotificationKind string

// Supported notification adapter kinds.
const (
	NotificationKindWebhook NotificationKind = "webhook"
	NotificationKindDiscord NotificationKind = "discord"
	NotificationKindEmail   NotificationKind = "email"
)

// Valid reports whether the kind belongs to the closed supported set.
func (k NotificationKind) Valid() bool {
	return k == NotificationKindWebhook || k == NotificationKindDiscord || k == NotificationKindEmail
}

// NotificationTLSMode selects SMTP transport security negotiation.
type NotificationTLSMode string

// Supported SMTP TLS modes.
const (
	NotificationTLSStartTLS NotificationTLSMode = "starttls"
	NotificationTLSImplicit NotificationTLSMode = "implicit"
)

// NotificationAuthMode selects SMTP authentication.
type NotificationAuthMode string

// Supported SMTP authentication modes.
const (
	NotificationAuthPlain NotificationAuthMode = "plain"
	NotificationAuthLogin NotificationAuthMode = "login"
)

// NotificationSettings contains the non-secret, kind-specific channel policy.
type NotificationSettings struct {
	AllowInsecure   bool
	AllowPrivate    bool
	SMTPPort        int
	TLSMode         NotificationTLSMode
	AuthMode        NotificationAuthMode
	Username        string
	FromAddress     string
	FromName        string
	Recipients      []string
	SubjectTemplate string
	BodyTemplate    string
}

// NotificationRegistration is safe to return through the API.
type NotificationRegistration struct {
	ID                  string
	Kind                NotificationKind
	Name                string
	Target              string
	Settings            NotificationSettings
	Subscriptions       []RequestEventType
	Enabled             bool
	DegradedAt          *time.Time
	ConsecutiveFailures int
	SecretSet           bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NotificationRecord adds the encrypted credential persistence fields.
type NotificationRecord struct {
	NotificationRegistration
	SecretCiphertext []byte
	KeyID            string
}

// NotificationPayload is the bounded structured request-event payload.
type NotificationPayload struct {
	DeliveryID        string           `json:"delivery_id,omitempty"`
	EventType         RequestEventType `json:"event_type"`
	RequestID         string           `json:"request_id"`
	Title             string           `json:"title"`
	Kind              MediaKind        `json:"kind"`
	Status            RequestStatus    `json:"status"`
	RequesterUsername string           `json:"requester_username"`
	ActorUsername     string           `json:"actor_username"`
	Reason            string           `json:"reason,omitempty"`
	OccurredAt        time.Time        `json:"occurred_at"`
	Test              bool             `json:"test"`
}

// RenderedNotification contains channel-ready text and structured data.
type RenderedNotification struct {
	Subject      string
	PlainBody    string
	MarkdownBody string
	Payload      NotificationPayload
}

// NotificationChannel is the consumer-owned delivery seam.
type NotificationChannel interface {
	Send(ctx context.Context, message RenderedNotification) error
	Probe(ctx context.Context) error
	Kind() NotificationKind
}

// NotificationFailureKind is a safe adapter failure classification.
type NotificationFailureKind string

// Safe notification adapter failure classes.
const (
	NotificationUnauthorized NotificationFailureKind = "unauthorized"
	NotificationUnavailable  NotificationFailureKind = "unavailable"
	NotificationRejected     NotificationFailureKind = "rejected"
	NotificationMalformed    NotificationFailureKind = "malformed"
)

// NotificationError classifies a delivery failure without exposing its destination.
type NotificationError struct {
	Kind       NotificationFailureKind
	Operation  string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *NotificationError) Error() string {
	return "notification " + e.Operation + ": " + string(e.Kind)
}

func (e *NotificationError) Unwrap() error { return e.Err }

// NotificationStatus is the closed durable delivery state.
type NotificationStatus string

// Durable notification delivery states.
const (
	NotificationPending NotificationStatus = "pending"
	NotificationSent    NotificationStatus = "sent"
	NotificationFailed  NotificationStatus = "failed"
)

// NotificationDelivery is one channel-specific durable outbox row.
type NotificationDelivery struct {
	ID             string
	ChannelID      string
	ChannelKind    NotificationKind
	EventType      RequestEventType
	Payload        NotificationPayload
	Status         NotificationStatus
	Attempts       int
	NextAttemptAt  time.Time
	LeaseToken     string
	LeaseExpiresAt *time.Time
	LastError      string
	SentAt         *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NotificationEvent is one committed request lifecycle event awaiting fan-out.
type NotificationEvent struct {
	ID    string
	Event RequestEvent
}

// NotificationDeliveryCursor is a stable recent-delivery boundary.
type NotificationDeliveryCursor struct {
	CreatedAt time.Time
	ID        string
}

// NotificationLease grants temporary ownership of one outbox row.
type NotificationLease struct {
	Token     string
	ExpiresAt time.Time
}

// NotificationChannelReader reads encrypted registrations and safe pages.
type NotificationChannelReader interface {
	GetNotificationChannel(ctx context.Context, id string) (NotificationRecord, error)
	ListNotificationChannels(ctx context.Context, afterNameKey string, pageSize int) ([]NotificationRegistration, error)
}

// NotificationChannelWriter persists channel registration changes.
type NotificationChannelWriter interface {
	CreateNotificationChannel(ctx context.Context, channel NotificationRecord) error
	UpdateNotificationChannel(ctx context.Context, channel NotificationRecord) error
	DeleteNotificationChannel(ctx context.Context, id string, at time.Time) error
}

// NotificationEventStore reads committed request events and fans them out atomically.
type NotificationEventStore interface {
	GetUnfannedNotificationEvent(ctx context.Context) (NotificationEvent, error)
	FanOutNotificationEvent(ctx context.Context, eventID string, payload NotificationPayload, at time.Time) (int, error)
}

// NotificationDeliveryStore owns lease-based delivery state transitions.
type NotificationDeliveryStore interface {
	ClaimNotificationDelivery(ctx context.Context, lease NotificationLease, at time.Time) (NotificationDelivery, error)
	CompleteNotificationDelivery(ctx context.Context, id, leaseToken string, at time.Time) error
	RescheduleNotificationDelivery(ctx context.Context, id, leaseToken, safeReason string, at, next time.Time, terminal bool) error
}

// NotificationDeliveryReader reads recent history and pending depth.
type NotificationDeliveryReader interface {
	ListNotificationDeliveries(ctx context.Context, channelID string, after *NotificationDeliveryCursor, pageSize int) ([]NotificationDelivery, error)
	NotificationOutboxDepth(ctx context.Context) (int64, error)
}

// NotificationMaintenanceStore tracks degradation, retention, and tombstone cleanup.
type NotificationMaintenanceStore interface {
	RecordNotificationChannelResult(ctx context.Context, channelID string, success, terminal bool, degradedAfter int, at time.Time) error
	PruneNotificationDeliveries(ctx context.Context, before time.Time, batchSize int) (int64, error)
	DeleteTombstonedNotificationChannels(ctx context.Context, at time.Time, batchSize int) (int64, error)
}

// ValidateNotificationRegistration checks a complete safe registration.
func ValidateNotificationRegistration(value NotificationRegistration) error {
	if !ValidID(value.ID) || !value.Kind.Valid() || ValidateMediaServerName(value.Name) != nil {
		return ErrInvalidArgument
	}
	if len(value.Target) > MaxNotificationTargetBytes || !utf8.ValidString(value.Target) {
		return ErrInvalidArgument
	}
	if err := ValidateNotificationSubscriptions(value.Subscriptions); err != nil {
		return err
	}
	if value.ConsecutiveFailures < 0 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return ErrInvalidArgument
	}
	return ValidateNotificationSettings(value.Kind, value.Target, value.Settings)
}

// ValidateNotificationSubscriptions checks a nonempty unique closed event set.
func ValidateNotificationSubscriptions(values []RequestEventType) error {
	if len(values) == 0 || len(values) > 6 {
		return ErrInvalidArgument
	}
	seen := make(map[RequestEventType]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return ErrInvalidArgument
		}
		seen[value] = struct{}{}
	}
	if len(seen) != len(values) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateNotificationSettings checks kind-specific non-secret settings.
func ValidateNotificationSettings(kind NotificationKind, target string, value NotificationSettings) error {
	if len(value.SubjectTemplate) > MaxNotificationTemplateBytes || len(value.BodyTemplate) > MaxNotificationTemplateBytes {
		return ErrInvalidArgument
	}
	if kind != NotificationKindEmail {
		if target != "" || value.SMTPPort != 0 || len(value.Recipients) != 0 || value.Username != "" || value.FromAddress != "" {
			return ErrInvalidArgument
		}
		return nil
	}
	if target == "" || strings.TrimSpace(target) != target || value.SMTPPort < 1 || value.SMTPPort > 65535 {
		return ErrInvalidArgument
	}
	if value.AllowInsecure {
		return ErrInvalidArgument
	}
	if value.TLSMode != NotificationTLSStartTLS && value.TLSMode != NotificationTLSImplicit {
		return ErrInvalidArgument
	}
	if value.AuthMode != NotificationAuthPlain && value.AuthMode != NotificationAuthLogin {
		return ErrInvalidArgument
	}
	if value.Username == "" || !validMailbox(value.FromAddress) || len(value.Recipients) == 0 || len(value.Recipients) > MaxNotificationRecipients {
		return ErrInvalidArgument
	}
	if err := ValidateNotificationRecipients(value.Recipients); err != nil {
		return err
	}
	if len(value.FromName) > 200 || strings.IndexFunc(value.FromName, unicode.IsControl) >= 0 {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateNotificationRecipients checks valid and case-insensitively unique mailboxes.
func ValidateNotificationRecipients(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validMailbox(value) {
			return ErrInvalidArgument
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return errors.Join(ErrInvalidArgument, ErrDuplicateNotificationRecipient)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validMailbox(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value
}

// ValidateNotificationPayload checks a bounded structured request event.
func ValidateNotificationPayload(value NotificationPayload) error {
	if value.DeliveryID != "" && !ValidID(value.DeliveryID) {
		return ErrInvalidArgument
	}
	if !value.EventType.Valid() || !ValidID(value.RequestID) || !value.Kind.Valid() ||
		!value.Status.Valid() || value.OccurredAt.IsZero() {
		return ErrInvalidArgument
	}
	fields := []string{value.Title, value.RequesterUsername, value.ActorUsername, value.Reason}
	for _, field := range fields {
		if len(field) > MaxNotificationTemplateBytes || !utf8.ValidString(field) {
			return ErrInvalidArgument
		}
	}
	return nil
}

// ValidateNotificationLease checks a future expiring lease.
func ValidateNotificationLease(lease NotificationLease, at time.Time) error {
	if !ValidID(lease.Token) || at.IsZero() || !lease.ExpiresAt.After(at) {
		return ErrInvalidArgument
	}
	return nil
}

// NotificationNameKey returns the canonical uniqueness and pagination key.
func NotificationNameKey(name string) string { return MediaServerNameKey(name) }

// SortedNotificationSubscriptions returns a stable copy of an event set.
func SortedNotificationSubscriptions(values []RequestEventType) []RequestEventType {
	result := append([]RequestEventType(nil), values...)
	slices.Sort(result)
	return result
}

// SafeNotificationReason removes destination and provider details from failures.
func SafeNotificationReason(err error) string {
	if classified, ok := errors.AsType[*NotificationError](err); ok {
		return fmt.Sprintf("delivery %s", classified.Kind)
	}
	return "delivery failed"
}
