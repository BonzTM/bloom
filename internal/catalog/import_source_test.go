package catalog

import (
	"context"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

type catalogUsersStub struct{ users []core.MediaUser }

func (s catalogUsersStub) Users(context.Context, string) ([]core.MediaUser, error) {
	return s.users, nil
}

func (catalogUsersStub) CatalogUserData(
	context.Context, string, string, []string,
) ([]core.LibraryUserData, error) {
	return nil, nil
}

func TestUserDataSourceEnumeratesEveryUpstreamUserByID(t *testing.T) {
	t.Parallel()
	source, err := NewUserDataSource(&fakeCatalogStore{}, catalogUsersStub{users: []core.MediaUser{
		{ID: "user-z", Name: "Unlinked"}, {ID: "user-a", Name: "Linked"}, {ID: "user-z", Name: "Unlinked"},
	}})
	if err != nil {
		t.Fatalf("NewUserDataSource: %v", err)
	}
	first, err := source.NextCatalogUser(t.Context(), catalogWorkerServerID, "")
	if err != nil || first.MediaUserID != "user-a" {
		t.Fatalf("first user = %+v, %v", first, err)
	}
	second, err := source.NextCatalogUser(t.Context(), catalogWorkerServerID, first.MediaUserID)
	if err != nil || second.MediaUserID != "user-z" || second.Username != "Unlinked" {
		t.Fatalf("unlinked user = %+v, %v", second, err)
	}
}
