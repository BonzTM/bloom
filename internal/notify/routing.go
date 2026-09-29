package notify

import (
	"context"
	"errors"

	"github.com/BonzTM/bloom/internal/core"
)

// RoutingService owns account preferences and title subscriptions.
type RoutingService struct {
	preferences   core.NotificationPreferenceStore
	subscriptions core.TitleSubscriptionStore
	clock         core.Clock
}

// NewRoutingService constructs account-scoped notification routing policy.
func NewRoutingService(
	preferences core.NotificationPreferenceStore,
	subscriptions core.TitleSubscriptionStore,
	clock core.Clock,
) (*RoutingService, error) {
	if preferences == nil || subscriptions == nil || clock == nil {
		return nil, errors.New("notification routing service: all dependencies are required")
	}
	return &RoutingService{
		preferences: preferences, subscriptions: subscriptions, clock: clock,
	}, nil
}

// Preferences returns the effective routing matrix for one account.
func (s *RoutingService) Preferences(ctx context.Context, accountID string) ([]core.NotificationPreference, error) {
	stored, err := s.preferences.ListNotificationPreferences(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return effectivePreferences(stored), nil
}

// UpdatePreferences validates and replaces one account's full preference matrix.
func (s *RoutingService) UpdatePreferences(
	ctx context.Context, accountID string, values []core.NotificationPreference,
) ([]core.NotificationPreference, error) {
	if err := core.ValidateNotificationPreferences(values); err != nil {
		return nil, err
	}
	if err := s.preferences.ReplaceNotificationPreferences(ctx, accountID, values); err != nil {
		return nil, err
	}
	return effectivePreferences(values), nil
}

func effectivePreferences(stored []core.NotificationPreference) []core.NotificationPreference {
	selected := make(map[core.RequestEventType]bool, len(core.NotificationEventTypes()))
	for _, eventType := range core.NotificationEventTypes() {
		selected[eventType] = true
	}
	for _, preference := range stored {
		selected[preference.EventType] = preference.Enabled
	}
	values := make([]core.NotificationPreference, 0, len(selected))
	for _, eventType := range core.NotificationEventTypes() {
		values = append(values, core.NotificationPreference{EventType: eventType, Enabled: selected[eventType]})
	}
	return values
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
