package downloadmanager

import (
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
	"github.com/BonzTM/bloom/internal/downloadmanager/radarr"
	"github.com/BonzTM/bloom/internal/downloadmanager/sonarr"
)

// Registry constructs the adapter selected by a persisted manager kind.
type Registry struct {
	observer arr.Observer
}

// NewRegistry creates a registry using observer for all external calls.
func NewRegistry(observer arr.Observer) *Registry { return &Registry{observer: observer} }

// New constructs one validated adapter.
func (r *Registry) New(
	kind core.DownloadManagerKind, baseURL, credential string, allowInsecure bool,
) (core.DownloadManagerAdapter, error) {
	switch kind {
	case core.DownloadManagerKindRadarr:
		return radarr.New(radarr.Config{
			BaseURL: baseURL, APIKey: credential, AllowInsecure: allowInsecure, Observer: r.observer,
		})
	case core.DownloadManagerKindSonarr:
		return sonarr.New(sonarr.Config{
			BaseURL: baseURL, APIKey: credential, AllowInsecure: allowInsecure, Observer: r.observer,
		})
	default:
		return nil, fmt.Errorf("download manager kind %q: %w", kind, core.ErrInvalidArgument)
	}
}
