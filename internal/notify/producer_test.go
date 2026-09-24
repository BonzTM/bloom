package notify

import (
	"context"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type producerAccounts map[string]core.Account

func (a producerAccounts) CreateAccount(context.Context, core.Account) error { return nil }
func (a producerAccounts) GetAccount(_ context.Context, id string) (core.Account, error) {
	value, ok := a[id]
	if !ok {
		return core.Account{}, core.ErrNotFound
	}
	return value, nil
}

func (a producerAccounts) GetAccountByUsername(context.Context, string) (core.Account, error) {
	return core.Account{}, core.ErrNotFound
}

func TestNotificationPayloadEnrichesRequestEventWithoutNetworkWork(t *testing.T) {
	t.Parallel()
	accounts := producerAccounts{
		"requester": {ID: "requester", Username: "alice"},
		"actor":     {ID: "actor", Username: "admin"},
	}
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	payload := notificationPayload(t.Context(), accounts, core.RequestEvent{
		Type: core.RequestEventApproved, RequestID: "00000000-0000-4000-8000-000000000001",
		RequesterID: "requester", ActorID: "actor", Title: "Example", Kind: core.MediaKindMovie,
		Status: core.RequestApproved, Reason: "okay", At: at,
	})
	if payload.RequesterUsername != "alice" || payload.ActorUsername != "admin" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestNotificationPayloadFallsBackWhenEnrichmentFails(t *testing.T) {
	t.Parallel()
	payload := notificationPayload(t.Context(), producerAccounts{}, core.RequestEvent{RequesterID: "missing", ActorID: "missing"})
	if payload.RequesterUsername != "" || payload.ActorUsername != "" {
		t.Fatalf("payload = %+v", payload)
	}
}
