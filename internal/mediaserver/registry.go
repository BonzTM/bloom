package mediaserver

import (
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/mediaserver/jellyfin"
)

// Registry constructs only explicitly supported media-server kinds.
type Registry struct {
	version  string
	deviceID string
	observer jellyfin.Observer
}

// NewRegistry returns the compiled adapter registry.
func NewRegistry(version, deviceID string, observer jellyfin.Observer) *Registry {
	return &Registry{version: version, deviceID: deviceID, observer: observer}
}

// New constructs the requested adapter and rejects unknown kinds.
func (r *Registry) New(
	kind core.MediaServerKind,
	baseURL, credential string,
	allowInsecure bool,
) (core.MediaServerAdapter, error) {
	switch kind {
	case core.MediaServerKindJellyfin:
		return jellyfin.New(jellyfin.Config{
			BaseURL: baseURL, APIKey: credential, Version: r.version, DeviceID: r.deviceID,
			AllowInsecure: allowInsecure, Observer: r.observer,
		})
	default:
		return nil, fmt.Errorf("media server kind %q: %w", kind, core.ErrInvalidArgument)
	}
}

// Capabilities returns the static capability set for a registered kind.
func (*Registry) Capabilities(kind core.MediaServerKind) (core.Capabilities, error) {
	switch kind {
	case core.MediaServerKindJellyfin:
		return core.Capabilities{CreateUserWithPassword: true, SetPassword: true, QuickConnectApproval: true, ProviderIDLookup: true}, nil
	default:
		return core.Capabilities{}, fmt.Errorf("media server kind %q: %w", kind, core.ErrInvalidArgument)
	}
}
