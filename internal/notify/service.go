// Package notify coordinates encrypted channel registrations and durable delivery.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

const credentialPurpose = "notification-channel-credential"

type credentialCipher interface {
	Encrypt([]byte, secrets.Context) ([]byte, error)
	Decrypt([]byte, secrets.Context) ([]byte, error)
	KeyID() string
}

type adapterFactory interface {
	New(core.NotificationRegistration, Credentials) (core.NotificationChannel, error)
}

// RegistrationInput contains one complete channel replacement.
type RegistrationInput struct {
	Kind          core.NotificationKind
	Name          string
	Target        string
	Settings      core.NotificationSettings
	Subscriptions []core.RequestEventType
	Enabled       bool
	Credentials   *Credentials
}

// Service manages encrypted notification-channel registrations.
type Service struct {
	reader     core.NotificationChannelReader
	writer     core.NotificationChannelWriter
	deliveries core.NotificationDeliveryReader
	cipher     credentialCipher
	factory    adapterFactory
	clock      core.Clock
}

// NewService constructs a notification registration service.
func NewService(
	reader core.NotificationChannelReader, writer core.NotificationChannelWriter,
	deliveries core.NotificationDeliveryReader, cipher credentialCipher, factory adapterFactory, clock core.Clock,
) (*Service, error) {
	if reader == nil || writer == nil || deliveries == nil || cipher == nil || factory == nil || clock == nil {
		return nil, errors.New("notification service: all dependencies are required")
	}
	return &Service{reader: reader, writer: writer, deliveries: deliveries, cipher: cipher, factory: factory, clock: clock}, nil
}

// Register probes and stores a new channel.
func (s *Service) Register(ctx context.Context, input RegistrationInput) (core.NotificationRegistration, error) {
	if input.Credentials == nil {
		return core.NotificationRegistration{}, core.ErrInvalidArgument
	}
	record, credentials, err := s.newRecord(input)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	adapter, err := s.factory.New(record.NotificationRegistration, credentials)
	if err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("construct notification channel: %w", err)
	}
	defer closeChannel(adapter)
	if err := adapter.Probe(ctx); err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("probe notification channel: %w", err)
	}
	if err := s.writer.CreateNotificationChannel(ctx, record); err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("store notification channel: %w", err)
	}
	return record.NotificationRegistration, nil
}

func (s *Service) newRecord(input RegistrationInput) (core.NotificationRecord, Credentials, error) {
	id, err := core.NewID()
	if err != nil {
		return core.NotificationRecord{}, Credentials{}, fmt.Errorf("create notification channel id: %w", err)
	}
	now := core.NormalizeTime(s.clock.Now())
	registration := core.NotificationRegistration{
		ID: id, Kind: input.Kind, Name: input.Name, Target: input.Target, Settings: input.Settings,
		Subscriptions: append([]core.RequestEventType(nil), input.Subscriptions...), Enabled: input.Enabled,
		SecretSet: true, CreatedAt: now, UpdatedAt: now,
	}
	if validationErr := validateRegistrationInput(registration, *input.Credentials); validationErr != nil {
		return core.NotificationRecord{}, Credentials{}, validationErr
	}
	ciphertext, err := s.encryptCredentials(registration, *input.Credentials)
	if err != nil {
		return core.NotificationRecord{}, Credentials{}, err
	}
	return core.NotificationRecord{
		NotificationRegistration: registration, SecretCiphertext: ciphertext, KeyID: s.cipher.KeyID(),
	}, *input.Credentials, nil
}

func validateRegistrationInput(registration core.NotificationRegistration, credentials Credentials) error {
	if err := core.ValidateNotificationRegistration(registration); err != nil {
		return err
	}
	if err := ValidateTemplates(registration.Settings.SubjectTemplate, registration.Settings.BodyTemplate); err != nil {
		return err
	}
	if len(credentials.WebhookURL)+len(credentials.SharedSecret)+len(credentials.DiscordURL)+len(credentials.Password) > 8192 {
		return core.ErrInvalidArgument
	}
	switch registration.Kind {
	case core.NotificationKindWebhook:
		if credentials.WebhookURL == "" || credentials.SharedSecret == "" || credentials.DiscordURL != "" || credentials.Password != "" {
			return core.ErrInvalidArgument
		}
	case core.NotificationKindDiscord:
		if credentials.DiscordURL == "" || credentials.WebhookURL != "" || credentials.SharedSecret != "" || credentials.Password != "" {
			return core.ErrInvalidArgument
		}
	case core.NotificationKindEmail:
		if credentials.Password == "" || credentials.WebhookURL != "" || credentials.SharedSecret != "" || credentials.DiscordURL != "" {
			return core.ErrInvalidArgument
		}
	default:
		return core.ErrInvalidArgument
	}
	return nil
}

func (s *Service) encryptCredentials(registration core.NotificationRegistration, credentials Credentials) ([]byte, error) {
	encoded, err := json.Marshal(credentials) //nolint:gosec // cleared after immediate authenticated encryption.
	if err != nil {
		return nil, fmt.Errorf("encode notification credentials: %w", err)
	}
	defer clear(encoded)
	ciphertext, err := s.cipher.Encrypt(encoded, credentialContext(registration))
	if err != nil {
		return nil, fmt.Errorf("encrypt notification credentials: %w", err)
	}
	return ciphertext, nil
}

// Get returns one secret-free channel registration.
func (s *Service) Get(ctx context.Context, id string) (core.NotificationRegistration, error) {
	record, err := s.reader.GetNotificationChannel(ctx, id)
	if err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("get notification channel: %w", err)
	}
	return record.NotificationRegistration, nil
}

// List returns a bounded name-ordered channel page.
func (s *Service) List(ctx context.Context, after string, size int) ([]core.NotificationRegistration, error) {
	values, err := s.reader.ListNotificationChannels(ctx, after, size)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	return values, nil
}

// Update probes and replaces a channel while retaining omitted credentials.
func (s *Service) Update(ctx context.Context, id string, input RegistrationInput) (core.NotificationRegistration, error) {
	existing, err := s.reader.GetNotificationChannel(ctx, id)
	if err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("get notification channel: %w", err)
	}
	if input.Kind != existing.Kind {
		return core.NotificationRegistration{}, core.ErrInvalidArgument
	}
	credentials, err := s.credentials(existing)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	if input.Credentials != nil {
		credentials = *input.Credentials
	}
	updated := existing
	updated.Name, updated.Target, updated.Settings = input.Name, input.Target, input.Settings
	updated.Subscriptions = append([]core.RequestEventType(nil), input.Subscriptions...)
	updated.Enabled, updated.UpdatedAt = input.Enabled, core.NormalizeTime(s.clock.Now())
	if validationErr := validateRegistrationInput(updated.NotificationRegistration, credentials); validationErr != nil {
		return core.NotificationRegistration{}, validationErr
	}
	if input.Credentials != nil {
		updated.SecretCiphertext, err = s.encryptCredentials(updated.NotificationRegistration, credentials)
		if err != nil {
			return core.NotificationRegistration{}, err
		}
		updated.KeyID = s.cipher.KeyID()
	}
	if updated.Enabled {
		if err := s.probe(ctx, updated.NotificationRegistration, credentials); err != nil {
			return core.NotificationRegistration{}, err
		}
	}
	if err := s.writer.UpdateNotificationChannel(ctx, updated); err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("update notification channel: %w", err)
	}
	return updated.NotificationRegistration, nil
}

func (s *Service) probe(ctx context.Context, registration core.NotificationRegistration, credentials Credentials) error {
	adapter, err := s.factory.New(registration, credentials)
	if err != nil {
		return fmt.Errorf("construct notification channel: %w", err)
	}
	defer closeChannel(adapter)
	if err := adapter.Probe(ctx); err != nil {
		return fmt.Errorf("probe notification channel: %w", err)
	}
	return nil
}

// Delete tombstones a channel and returns safe metadata for auditing.
func (s *Service) Delete(ctx context.Context, id string) (core.NotificationRegistration, error) {
	record, err := s.reader.GetNotificationChannel(ctx, id)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	if err := s.writer.DeleteNotificationChannel(ctx, id, core.NormalizeTime(s.clock.Now())); err != nil {
		return core.NotificationRegistration{}, fmt.Errorf("delete notification channel: %w", err)
	}
	return record.NotificationRegistration, nil
}

// Test sends a test message immediately.
func (s *Service) Test(ctx context.Context, id string) error {
	record, adapter, err := s.channel(ctx, id)
	if err != nil {
		return err
	}
	defer closeChannel(adapter)
	deliveryID, err := core.NewID()
	if err != nil {
		return fmt.Errorf("create test delivery id: %w", err)
	}
	payload := core.NotificationPayload{
		DeliveryID: deliveryID,
		EventType:  core.RequestEventCreated, RequestID: "00000000-0000-4000-8000-000000000000",
		Title: "Bloom test notification", Kind: core.MediaKindMovie, Status: core.RequestPending,
		RequesterUsername: "bloom", ActorUsername: "bloom", OccurredAt: core.NormalizeTime(s.clock.Now()), Test: true,
	}
	message, err := RenderMessage(payload, record.Settings)
	if err != nil {
		return err
	}
	if err := adapter.Send(ctx, message); err != nil {
		return fmt.Errorf("send notification test: %w", err)
	}
	return nil
}

// Channel resolves and decrypts a registered delivery adapter.
func (s *Service) Channel(ctx context.Context, id string) (core.NotificationRegistration, core.NotificationChannel, error) {
	return s.channel(ctx, id)
}

func (s *Service) channel(ctx context.Context, id string) (core.NotificationRegistration, core.NotificationChannel, error) {
	record, err := s.reader.GetNotificationChannel(ctx, id)
	if err != nil {
		return core.NotificationRegistration{}, nil, fmt.Errorf("get notification channel: %w", err)
	}
	if !record.Enabled {
		return core.NotificationRegistration{}, nil, &core.NotificationError{
			Kind: core.NotificationRejected, Operation: "resolve", Retryable: false,
			Err: errors.New("channel disabled"),
		}
	}
	credentials, err := s.credentials(record)
	if err != nil {
		return core.NotificationRegistration{}, nil, err
	}
	adapter, err := s.factory.New(record.NotificationRegistration, credentials)
	if err != nil {
		return core.NotificationRegistration{}, nil, fmt.Errorf("construct notification channel: %w", err)
	}
	return record.NotificationRegistration, adapter, nil
}

func (s *Service) credentials(record core.NotificationRecord) (Credentials, error) {
	plaintext, err := s.cipher.Decrypt(record.SecretCiphertext, credentialContext(record.NotificationRegistration))
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt notification credentials: %w", err)
	}
	defer clear(plaintext)
	var credentials Credentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return Credentials{}, fmt.Errorf("decode notification credentials: %w", err)
	}
	return credentials, nil
}

// Deliveries returns a bounded recent-delivery page.
func (s *Service) Deliveries(ctx context.Context, channelID string, after *core.NotificationDeliveryCursor, size int) ([]core.NotificationDelivery, error) {
	if _, err := s.reader.GetNotificationChannel(ctx, channelID); err != nil {
		return nil, err
	}
	return s.deliveries.ListNotificationDeliveries(ctx, channelID, after, size)
}

func credentialContext(value core.NotificationRegistration) secrets.Context {
	return secrets.Context{
		Purpose: credentialPurpose, RecordID: value.ID, Kind: string(value.Kind),
		BaseURL: "notification-channel:" + string(value.Kind),
	}
}

func closeChannel(channel core.NotificationChannel) {
	if closer, ok := channel.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
