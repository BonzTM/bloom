package ratings

import "github.com/BonzTM/bloom/internal/ratings/omdb"

// OMDbFactory builds OMDb clients that share metrics and a clock.
type OMDbFactory struct {
	deps omdb.Dependencies
}

// NewOMDbFactory creates a factory for clients built with deps.
func NewOMDbFactory(deps omdb.Dependencies) OMDbFactory { return OMDbFactory{deps: deps} }

// New constructs an OMDb client for key.
func (f OMDbFactory) New(key string) (Client, error) {
	client, err := omdb.New(key, f.deps)
	if err != nil {
		return nil, err
	}
	return client, nil
}
