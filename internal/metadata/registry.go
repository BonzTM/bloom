package metadata

import (
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/metadata/tmdb"
)

// Registry constructs supported metadata provider adapters.
type Registry struct {
	deps tmdb.Dependencies
}

// NewRegistry creates a provider registry with shared TMDB dependencies.
func NewRegistry(deps tmdb.Dependencies) *Registry { return &Registry{deps: deps} }

// New constructs a provider for kind using the decrypted credential.
func (r *Registry) New(kind core.MetadataProviderKind, credential string) (core.MetadataProvider, error) {
	if kind != core.MetadataProviderTMDB {
		return nil, fmt.Errorf("metadata provider kind %q: %w", kind, core.ErrInvalidArgument)
	}
	return tmdb.New(credential, r.deps)
}
