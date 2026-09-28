package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/core"
)

// RoutingService owns account preferences and title subscriptions.
type RoutingService struct {
	channels      core.NotificationChannelReader
	preferences   core.NotificationPreferenceStore
	subscriptions core.TitleSubscriptionStore
	clock         core.Clock
}

// NewRoutingService constructs account-scoped notification routing policy.
func NewRoutingService(
	channels core.NotificationChannelReader,
	preferences core.NotificationPreferenceStore,
	subscriptions core.TitleSubscriptionStore,
	clock core.Clock,
) (*RoutingService, error) {
	if channels == nil || preferences == nil || subscriptions == nil || clock == nil {
		return nil, errors.New("notification routing service: all dependencies are required")
	}
	return &RoutingService{
		channels: channels, preferences: preferences, subscriptions: subscriptions, clock: clock,
	}, nil
}

// Preferences returns the effective routing matrix for one account.
func (s *RoutingService) Preferences(ctx context.Context, accountID string) (core.NotificationPreferences, error) {
	channels, err := s.channels.ListNotificationChannels(ctx, "", core.MaxNotificationChannels+1)
	if err != nil {
		return core.NotificationPreferences{}, fmt.Errorf("list preference channels: %w", err)
	}
	if len(channels) > core.MaxNotificationChannels {
		return core.NotificationPreferences{}, core.ErrInvalidArgument
	}
	overrides, err := s.preferences.ListNotificationPreferenceOverrides(ctx, accountID)
	if err != nil {
		return core.NotificationPreferences{}, err
	}
	return effectivePreferences(channels, overrides), nil
}

// UpdatePreferences validates and replaces one account's full preference matrix.
func (s *RoutingService) UpdatePreferences(
	ctx context.Context, accountID string, values []core.NotificationPreference,
) (core.NotificationPreferences, error) {
	if err := core.ValidateNotificationPreferences(values); err != nil {
		return core.NotificationPreferences{}, err
	}
	channels, err := s.channels.ListNotificationChannels(ctx, "", core.MaxNotificationChannels+1)
	if err != nil {
		return core.NotificationPreferences{}, err
	}
	if len(channels) > core.MaxNotificationChannels || !preferenceChannelsExist(channels, values) {
		return core.NotificationPreferences{}, core.ErrInvalidArgument
	}
	overrides := preferenceOverrides(channels, values)
	if err := s.preferences.ReplaceNotificationPreferenceOverrides(ctx, accountID, overrides); err != nil {
		return core.NotificationPreferences{}, err
	}
	return effectivePreferences(channels, overrides), nil
}

func effectivePreferences(
	channels []core.NotificationRegistration, overrides []core.NotificationPreferenceOverride,
) core.NotificationPreferences {
	selected := make(map[core.RequestEventType]map[string]bool, len(core.NotificationEventTypes()))
	for _, eventType := range core.NotificationEventTypes() {
		selected[eventType] = make(map[string]bool, len(channels))
		for _, channel := range channels {
			selected[eventType][channel.ID] = eventType != core.NotificationEventPlaybackSessionStarted
		}
	}
	for _, override := range overrides {
		selected[override.EventType][override.ChannelID] = override.Enabled
	}
	values := make([]core.NotificationPreference, 0, len(selected))
	for _, eventType := range core.NotificationEventTypes() {
		ids := make([]string, 0, len(channels))
		for _, channel := range channels {
			if selected[eventType][channel.ID] {
				ids = append(ids, channel.ID)
			}
		}
		values = append(values, core.NotificationPreference{EventType: eventType, ChannelIDs: ids})
	}
	return core.NotificationPreferences{Channels: channels, Preferences: values}
}

func preferenceChannelsExist(
	channels []core.NotificationRegistration, values []core.NotificationPreference,
) bool {
	known := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		known[channel.ID] = struct{}{}
	}
	for _, value := range values {
		for _, id := range value.ChannelIDs {
			if _, exists := known[id]; !exists {
				return false
			}
		}
	}
	return true
}

func preferenceOverrides(
	channels []core.NotificationRegistration, values []core.NotificationPreference,
) []core.NotificationPreferenceOverride {
	result := make([]core.NotificationPreferenceOverride, 0, len(channels)*len(values))
	for _, value := range values {
		for _, channel := range channels {
			result = append(result, core.NotificationPreferenceOverride{
				EventType: value.EventType, ChannelID: channel.ID,
				Enabled: slices.Contains(value.ChannelIDs, channel.ID),
			})
		}
	}
	return result
}

// SubscribeTitle idempotently follows one provider title.
func (s *RoutingService) SubscribeTitle(
	ctx context.Context, accountID string, provider core.MetadataProviderKind, providerID string,
) error {
	return s.subscriptions.SubscribeTitle(ctx, core.TitleSubscription{
		AccountID: accountID, Provider: provider, ProviderID: providerID,
		CreatedAt: core.NormalizeTime(s.clock.Now()),
	})
}

// UnsubscribeTitle idempotently removes one provider-title follow.
func (s *RoutingService) UnsubscribeTitle(
	ctx context.Context, accountID string, provider core.MetadataProviderKind, providerID string,
) error {
	return s.subscriptions.UnsubscribeTitle(ctx, accountID, provider, providerID)
}

// TitleSubscribed reports whether one account follows a provider title.
func (s *RoutingService) TitleSubscribed(
	ctx context.Context, accountID string, provider core.MetadataProviderKind, providerID string,
) (bool, error) {
	return s.subscriptions.TitleSubscribed(ctx, accountID, provider, providerID)
}
