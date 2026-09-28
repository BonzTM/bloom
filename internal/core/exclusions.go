package core

import (
	"context"
	"fmt"
	"slices"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxExclusionIDBytes bounds one upstream user or library identifier.
	MaxExclusionIDBytes = 128
	// MaxMediaServerExclusions bounds a full replacement for one server.
	MaxMediaServerExclusions = 500
)

// MediaServerExclusions is the complete collection exclusion set for one server.
type MediaServerExclusions struct {
	MediaServerID string
	MediaUserIDs  []string
	LibraryIDs    []string
}

// Valid reports whether the full replacement is normalized and bounded.
func (e MediaServerExclusions) Valid() bool {
	if !ValidID(e.MediaServerID) || len(e.MediaUserIDs)+len(e.LibraryIDs) > MaxMediaServerExclusions {
		return false
	}
	return validExclusionIDs(e.MediaUserIDs) && validExclusionIDs(e.LibraryIDs)
}

func validExclusionIDs(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > MaxExclusionIDBytes || !utf8.ValidString(value) ||
			unicodeContainsControl(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func unicodeContainsControl(value string) bool {
	return slices.ContainsFunc([]rune(value), unicode.IsControl)
}

// ExcludesUser reports whether collection is disabled for a media user.
func (e MediaServerExclusions) ExcludesUser(id string) bool {
	return slices.Contains(e.MediaUserIDs, id)
}

// ExcludesLibrary reports whether collection is disabled for a library.
func (e MediaServerExclusions) ExcludesLibrary(id string) bool {
	return id != "" && slices.Contains(e.LibraryIDs, id)
}

// Clone returns a copy safe to retain across cache boundaries.
func (e MediaServerExclusions) Clone() MediaServerExclusions {
	e.MediaUserIDs = slices.Clone(e.MediaUserIDs)
	e.LibraryIDs = slices.Clone(e.LibraryIDs)
	return e
}

// ExclusionStore persists full per-server exclusion sets.
type ExclusionStore interface {
	GetExclusions(context.Context, string) (MediaServerExclusions, error)
	ReplaceExclusions(context.Context, MediaServerExclusions) error
}

// ExclusionReader supplies cached exclusion lookups to collection workers.
type ExclusionReader interface {
	GetExclusions(context.Context, string) (MediaServerExclusions, error)
}

// ValidateExclusionID returns a field-friendly validation error.
func ValidateExclusionID(value string) error {
	if !validExclusionIDs([]string{value}) {
		return fmt.Errorf("exclusion id: %w", ErrInvalidArgument)
	}
	return nil
}
