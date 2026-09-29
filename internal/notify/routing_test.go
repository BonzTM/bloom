package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type routingPreferences struct {
	values []core.NotificationPreference
	writes int
}

func (r *routingPreferences) ListNotificationPreferences(
	context.Context, string,
) ([]core.NotificationPreference, error) {
	return r.values, nil
}

func (r *routingPreferences) ReplaceNotificationPreferences(
	_ context.Context, _ string, values []core.NotificationPreference,
) error {
	r.values = values
	r.writes++
	return nil
}

type routingTitles struct{}

func (routingTitles) SubscribeTitle(context.Context, core.TitleSubscription) error { return nil }
func (routingTitles) UnsubscribeTitle(context.Context, string, core.MetadataProviderKind, string) error {
	return nil
}

func (routingTitles) TitleSubscribed(context.Context, string, core.MetadataProviderKind, string) (bool, error) {
	return false, nil
}

func TestRoutingPreferencesDefaultEveryEventOn(t *testing.T) {
	store := &routingPreferences{}
	service := newRoutingTestService(t, store)
	got, err := service.Preferences(t.Context(), testRoutingID(3))
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	if len(got) != len(core.NotificationEventTypes()) {
		t.Fatalf("preference count = %d", len(got))
	}
	for _, preference := range got {
		if !preference.Enabled {
			t.Errorf("%s enabled = false, want true", preference.EventType)
		}
	}
}

func TestRoutingPreferencesPersistCompleteMatrix(t *testing.T) {
	store := &routingPreferences{}
	service := newRoutingTestService(t, store)
	invalid := completeRoutingPreferences()
	invalid[0].EventType = invalid[1].EventType
	if _, err := service.UpdatePreferences(t.Context(), testRoutingID(3), invalid); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("duplicate event update = %v, want ErrInvalidArgument", err)
	}
	if store.writes != 0 {
		t.Fatalf("invalid update writes = %d", store.writes)
	}
	values := completeRoutingPreferences()
	values[len(values)-1].Enabled = false
	got, err := service.UpdatePreferences(t.Context(), testRoutingID(3), values)
	if err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}
	if store.writes != 1 || len(store.values) != len(core.NotificationEventTypes()) {
		t.Fatalf("persisted preferences = %d across %d writes", len(store.values), store.writes)
	}
	playback := got[len(got)-1]
	if playback.EventType != core.NotificationEventPlaybackSessionStarted || playback.Enabled {
		t.Fatalf("playback preference = %+v", playback)
	}
}

func newRoutingTestService(
	t *testing.T, preferences *routingPreferences,
) *RoutingService {
	t.Helper()
	service, err := NewRoutingService(
		preferences, routingTitles{},
		testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)),
	)
	if err != nil {
		t.Fatalf("NewRoutingService: %v", err)
	}
	return service
}

func completeRoutingPreferences() []core.NotificationPreference {
	values := make([]core.NotificationPreference, 0, len(core.NotificationEventTypes()))
	for _, eventType := range core.NotificationEventTypes() {
		values = append(values, core.NotificationPreference{EventType: eventType, Enabled: true})
	}
	return values
}

func testRoutingID(last int) string {
	return "00000000-0000-4000-8000-00000000000" + string(rune('0'+last))
}
