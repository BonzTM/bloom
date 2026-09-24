package notify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

type serviceStore struct {
	record  core.NotificationRecord
	updated bool
	deleted bool
}

func (s *serviceStore) GetNotificationChannel(context.Context, string) (core.NotificationRecord, error) {
	if s.deleted {
		return core.NotificationRecord{}, core.ErrNotFound
	}
	return s.record, nil
}

func (s *serviceStore) ListNotificationChannels(context.Context, string, int) ([]core.NotificationRegistration, error) {
	if s.deleted {
		return []core.NotificationRegistration{}, nil
	}
	return []core.NotificationRegistration{s.record.NotificationRegistration}, nil
}

func (*serviceStore) CreateNotificationChannel(context.Context, core.NotificationRecord) error {
	return nil
}

func (s *serviceStore) UpdateNotificationChannel(_ context.Context, record core.NotificationRecord) error {
	s.record, s.updated = record, true
	return nil
}

func (s *serviceStore) DeleteNotificationChannel(context.Context, string, time.Time) error {
	s.deleted = true
	return nil
}

func (*serviceStore) ListNotificationDeliveries(
	context.Context, string, *core.NotificationDeliveryCursor, int,
) ([]core.NotificationDelivery, error) {
	return nil, nil
}

func (*serviceStore) NotificationOutboxDepth(context.Context) (int64, error) { return 0, nil }

type serviceCipher struct{ plaintext []byte }

func (serviceCipher) Encrypt(value []byte, _ secrets.Context) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func (c serviceCipher) Decrypt([]byte, secrets.Context) ([]byte, error) {
	return append([]byte(nil), c.plaintext...), nil
}

func (serviceCipher) KeyID() string { return "key-1" }

type serviceFactory struct{ calls int }

func (f *serviceFactory) New(core.NotificationRegistration, Credentials) (core.NotificationChannel, error) {
	f.calls++
	return &workerChannel{err: errors.New("unreachable")}, nil
}

func TestUpdateCanDisableUnreachableChannelWithoutProbe(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &serviceStore{record: core.NotificationRecord{NotificationRegistration: core.NotificationRegistration{
		ID: "00000000-0000-4000-8000-000000000021", Kind: core.NotificationKindWebhook,
		Name: "Webhook", Subscriptions: []core.RequestEventType{core.RequestEventCreated}, Enabled: true,
		SecretSet: true, CreatedAt: at, UpdatedAt: at,
	}, SecretCiphertext: []byte("encrypted"), KeyID: "key-1"}}
	credentials, err := json.Marshal(Credentials{WebhookURL: "https://example.test/hook", SharedSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	factory := &serviceFactory{}
	service, err := NewService(store, store, store, serviceCipher{plaintext: credentials}, factory, fixedServiceClock{at})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(t.Context(), store.record.ID, RegistrationInput{
		Kind: core.NotificationKindWebhook, Name: "Webhook",
		Subscriptions: []core.RequestEventType{core.RequestEventCreated}, Enabled: false,
	})
	if err != nil || updated.Enabled || !store.updated || factory.calls != 0 {
		t.Fatalf("Update = %+v, %v; stored=%v factory calls=%d", updated, err, store.updated, factory.calls)
	}
	_, _, err = service.Channel(t.Context(), store.record.ID)
	var classified *core.NotificationError
	if !errors.As(err, &classified) || classified.Retryable || factory.calls != 0 {
		t.Fatalf("Channel error = %#v; factory calls=%d", err, factory.calls)
	}
}

func TestDeleteHidesChannelFromServiceOperations(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	record := core.NotificationRecord{NotificationRegistration: core.NotificationRegistration{
		ID: "00000000-0000-4000-8000-000000000021", Kind: core.NotificationKindWebhook,
		Name: "Webhook", Subscriptions: []core.RequestEventType{core.RequestEventCreated}, Enabled: true,
		SecretSet: true, CreatedAt: at, UpdatedAt: at,
	}, SecretCiphertext: []byte("encrypted"), KeyID: "key-1"}
	store := &serviceStore{record: record}
	credentials, err := json.Marshal(Credentials{WebhookURL: "https://example.test/hook", SharedSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	factory := &serviceFactory{}
	service, err := NewService(store, store, store, serviceCipher{plaintext: credentials}, factory, fixedServiceClock{at})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Delete(t.Context(), record.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertDeletedServiceOperations(t, service, record.ID)
	if factory.calls != 0 {
		t.Fatalf("adapter factory calls = %d", factory.calls)
	}
}

func assertDeletedServiceOperations(t *testing.T, service *Service, id string) {
	t.Helper()
	if values, err := service.List(t.Context(), "", 10); err != nil || len(values) != 0 {
		t.Fatalf("List after delete = %+v, %v", values, err)
	}
	if _, err := service.Get(t.Context(), id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
	if _, err := service.Update(t.Context(), id, RegistrationInput{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Update after delete error = %v", err)
	}
	if err := service.Test(t.Context(), id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Test after delete error = %v", err)
	}
}

type fixedServiceClock struct{ at time.Time }

func (c fixedServiceClock) Now() time.Time { return c.at }
