package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type routingChannels struct {
	values []core.NotificationRegistration
}

func (r routingChannels) GetNotificationChannel(context.Context, string) (core.NotificationRecord, error) {
	return core.NotificationRecord{}, core.ErrNotFound
}

func (r routingChannels) ListNotificationChannels(context.Context, string, int) ([]core.NotificationRegistration, error) {
	return r.values, nil
}

type routingPreferences struct {
	overrides []core.NotificationPreferenceOverride
	writes    int
}

func (r *routingPreferences) ListNotificationPreferenceOverrides(
	context.Context, string,
) ([]core.NotificationPreferenceOverride, error) {
	return r.overrides, nil
}

func (r *routingPreferences) ReplaceNotificationPreferenceOverrides(
	_ context.Context, _ string, values []core.NotificationPreferenceOverride,
) error {
	r.overrides = values
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

func TestRoutingPreferencesDefaultRequestEventsOnAndPlaybackOff(t *testing.T) {
	channels := []core.NotificationRegistration{{ID: testRoutingID(1)}, {ID: testRoutingID(2)}}
	store := &routingPreferences{}
	service := newRoutingTestService(t, channels, store)
	got, err := service.Preferences(t.Context(), testRoutingID(3))
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	if len(got.Preferences) != len(core.NotificationEventTypes()) {
		t.Fatalf("preference count = %d", len(got.Preferences))
	}
	for _, preference := range got.Preferences {
		want := len(channels)
		if preference.EventType == core.NotificationEventPlaybackSessionStarted {
			want = 0
		}
		if len(preference.ChannelIDs) != want {
			t.Errorf("%s channel count = %d, want %d", preference.EventType, len(preference.ChannelIDs), want)
		}
	}
}

func TestRoutingPreferencesRejectUnknownChannelAndPersistPlaybackOptIn(t *testing.T) {
	channelID := testRoutingID(1)
	store := &routingPreferences{}
	service := newRoutingTestService(t, []core.NotificationRegistration{{ID: channelID}}, store)
	invalid := completeRoutingPreferences(channelID)
	invalid[0].ChannelIDs = []string{testRoutingID(9)}
	if _, err := service.UpdatePreferences(t.Context(), testRoutingID(3), invalid); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("unknown channel update = %v, want ErrInvalidArgument", err)
	}
	if store.writes != 0 {
		t.Fatalf("invalid update writes = %d", store.writes)
	}
	values := completeRoutingPreferences(channelID)
	got, err := service.UpdatePreferences(t.Context(), testRoutingID(3), values)
	if err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}
	if store.writes != 1 || len(store.overrides) != len(core.NotificationEventTypes()) {
		t.Fatalf("persisted overrides = %d across %d writes", len(store.overrides), store.writes)
	}
	playback := got.Preferences[len(got.Preferences)-1]
	if playback.EventType != core.NotificationEventPlaybackSessionStarted || len(playback.ChannelIDs) != 1 {
		t.Fatalf("playback preference = %+v", playback)
	}
}

func newRoutingTestService(
	t *testing.T, channels []core.NotificationRegistration, preferences *routingPreferences,
) *RoutingService {
	t.Helper()
	service, err := NewRoutingService(
		routingChannels{values: channels}, preferences, routingTitles{},
		testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)),
	)
	if err != nil {
		t.Fatalf("NewRoutingService: %v", err)
	}
	return service
}

func completeRoutingPreferences(channelID string) []core.NotificationPreference {
	values := make([]core.NotificationPreference, 0, len(core.NotificationEventTypes()))
	for _, eventType := range core.NotificationEventTypes() {
		values = append(values, core.NotificationPreference{EventType: eventType, ChannelIDs: []string{channelID}})
	}
	return values
}

func testRoutingID(last int) string {
	return "00000000-0000-4000-8000-00000000000" + string(rune('0'+last))
}
