package catalog

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/BonzTM/bloom/internal/core"
)

// UserDataSource joins catalog persistence with per-user adapter reads.
type UserDataSource struct {
	store  core.LibraryCatalogStore
	source interface {
		CatalogUserData(context.Context, string, string, []string) ([]core.LibraryUserData, error)
		Users(context.Context, string) ([]core.MediaUser, error)
	}
}

// NewUserDataSource validates and constructs the importer boundary.
func NewUserDataSource(store core.LibraryCatalogStore, source interface {
	CatalogUserData(context.Context, string, string, []string) ([]core.LibraryUserData, error)
	Users(context.Context, string) ([]core.MediaUser, error)
},
) (*UserDataSource, error) {
	if store == nil || source == nil {
		return nil, errors.New("catalog user-data source: dependencies are required")
	}
	return &UserDataSource{store: store, source: source}, nil
}

// ListCatalogImportItems returns one stable item-ID-ordered import page.
func (s *UserDataSource) ListCatalogImportItems(
	ctx context.Context, serverID, afterID string, limit int,
) ([]core.CatalogImportItem, error) {
	return s.store.ListCatalogImportItems(ctx, serverID, afterID, limit)
}

// NextCatalogUser returns the next upstream user in media-user ID order.
func (s *UserDataSource) NextCatalogUser(
	ctx context.Context, serverID, afterUserID string,
) (core.CatalogUser, error) {
	users, err := s.source.Users(ctx, serverID)
	if err != nil {
		return core.CatalogUser{}, err
	}
	slices.SortFunc(users, func(left, right core.MediaUser) int { return cmp.Compare(left.ID, right.ID) })
	for _, user := range users {
		if user.ID > afterUserID {
			return core.CatalogUser{MediaUserID: user.ID, Username: user.Name}, nil
		}
	}
	return core.CatalogUser{}, core.ErrNotFound
}

// CatalogUserData reads bounded per-user play state from the media server.
func (s *UserDataSource) CatalogUserData(
	ctx context.Context, serverID, userID string, itemIDs []string,
) ([]core.LibraryUserData, error) {
	return s.source.CatalogUserData(ctx, serverID, userID, itemIDs)
}
