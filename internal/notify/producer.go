package notify

import (
	"context"

	"github.com/BonzTM/bloom/internal/core"
)

// AccountReader resolves optional notification display names.
type AccountReader interface {
	GetAccount(context.Context, string) (core.Account, error)
}

func notificationPayload(ctx context.Context, accounts AccountReader, event core.NotificationEvent) core.NotificationPayload {
	if event.Playback != nil {
		return playbackNotificationPayload(*event.Playback)
	}
	return requestNotificationPayload(ctx, accounts, event.Event)
}

func requestNotificationPayload(ctx context.Context, accounts AccountReader, event core.RequestEvent) core.NotificationPayload {
	requester := accountUsername(ctx, accounts, event.RequesterID)
	actor := "system"
	if event.ActorID != "" && event.ActorID != "system" {
		if event.ActorID == event.RequesterID {
			actor = requester
		} else {
			actor = accountUsername(ctx, accounts, event.ActorID)
		}
	}
	return core.NotificationPayload{
		EventType: event.Type, RequestID: event.RequestID, Title: event.Title, Kind: event.Kind,
		Status: event.Status, RequesterUsername: requester, ActorUsername: actor,
		Reason: event.Reason, OccurredAt: core.NormalizeTime(event.At),
	}
}

func playbackNotificationPayload(event core.PlaybackSessionStartedEvent) core.NotificationPayload {
	return core.NotificationPayload{
		EventType: core.NotificationEventPlaybackSessionStarted, Title: event.ItemName,
		OccurredAt: core.NormalizeTime(event.At), WatchID: event.WatchID,
		MediaServerID: event.MediaServerID, MediaUserID: event.MediaUserID,
		MediaUsername: event.MediaUsername, ItemID: event.ItemID, ItemName: event.ItemName,
		ItemType: event.ItemType, Client: event.Client, DeviceID: event.DeviceID,
		DeviceName: event.DeviceName,
	}
}

func accountUsername(ctx context.Context, accounts AccountReader, id string) string {
	account, err := accounts.GetAccount(ctx, id)
	if err != nil {
		return ""
	}
	return account.Username
}
