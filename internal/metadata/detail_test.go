package metadata

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

const detailTestAccount = "11111111-1111-4111-8111-111111111111"

type detailTestProvider struct {
	serviceTestProvider
	detailCalls atomic.Int32
}

func detailTestDetails() core.MetadataDetails {
	related := func(id string) core.MetadataDiscoverItem {
		return core.MetadataDiscoverItem{MetadataTitle: core.MetadataTitle{
			Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: id, Title: "Film " + id,
		}, RequestState: core.MetadataRequestNone}
	}
	return core.MetadataDetails{
		Tagline: "Tagline", Cast: []core.MetadataCredit{{PersonID: 1, Name: "Actor"}},
		Recommendations: []core.MetadataDiscoverItem{related("11"), related("12")},
		Similar:         []core.MetadataDiscoverItem{related("13")},
	}
}

func (p *detailTestProvider) MovieDetail(_ context.Context, providerID string) (core.MetadataMovieDetail, error) {
	p.detailCalls.Add(1)
	return core.MetadataMovieDetail{MetadataTitle: core.MetadataTitle{
		Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: providerID, Title: "Film",
	}, Details: detailTestDetails()}, nil
}

func (p *detailTestProvider) SeriesDetail(
	_ context.Context, providerID string, includeSpecials bool,
) (core.MetadataSeriesDetail, error) {
	p.detailCalls.Add(1)
	if !includeSpecials {
		return core.MetadataSeriesDetail{}, errors.New("the service caches specials and filters them itself")
	}
	return core.MetadataSeriesDetail{MetadataSeries: core.MetadataSeries{
		MetadataTitle: core.MetadataTitle{
			Kind: core.MediaKindSeries, Provider: core.MetadataProviderTMDB, ProviderID: providerID, Title: "Show",
		},
		Seasons: []core.MetadataSeason{{Number: 0, Name: "Specials"}, {Number: 1, Name: "Season 1"}},
	}, Details: detailTestDetails()}, nil
}

func newDetailTestService(t *testing.T, provider core.MetadataProvider) (*Service, *serviceTestStates) {
	t.Helper()
	store := &serviceTestStore{record: core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: []byte(serviceTestReadAccessToken), KeyID: "test-key",
	}}
	states := &serviceTestStates{byAccount: map[string]core.RequestStatus{detailTestAccount: core.RequestAvailable}}
	return newServiceTestServiceWithStates(t, store, &serviceTestFactory{provider: provider}, states, slog.New(slog.DiscardHandler)), states
}

func TestMovieDetailCachesProviderAndMarksCallerRequestStates(t *testing.T) {
	provider := &detailTestProvider{}
	service, states := newDetailTestService(t, provider)
	first, err := service.MovieDetail(t.Context(), detailTestAccount, "99")
	if err != nil {
		t.Fatalf("MovieDetail: %v", err)
	}
	recommended := first.Details.Recommendations
	if recommended[0].RequestState != core.MetadataRequestAvailable || recommended[1].RequestState != core.MetadataRequestNone {
		t.Fatalf("recommendation states = %+v", recommended)
	}
	first.Details.Cast[0].Name = "Mutated"
	second, err := service.MovieDetail(t.Context(), "22222222-2222-4222-8222-222222222222", "99")
	if err != nil {
		t.Fatalf("MovieDetail again: %v", err)
	}
	if provider.detailCalls.Load() != 1 || second.Details.Cast[0].Name != "Actor" {
		t.Fatalf("provider calls = %d, cast = %+v", provider.detailCalls.Load(), second.Details.Cast)
	}
	if second.Details.Recommendations[0].RequestState != core.MetadataRequestPending || states.callCount() != 4 {
		t.Fatalf("second caller state = %s, state lookups = %d", second.Details.Recommendations[0].RequestState, states.callCount())
	}
}

func TestSeriesDetailFiltersSpecialsFromTheCachedSeries(t *testing.T) {
	provider := &detailTestProvider{}
	service, _ := newDetailTestService(t, provider)
	without, err := service.SeriesDetail(t.Context(), detailTestAccount, "7", false)
	if err != nil || len(without.Seasons) != 1 || without.Seasons[0].Number != 1 {
		t.Fatalf("SeriesDetail(no specials) = %+v, %v", without.Seasons, err)
	}
	with, err := service.SeriesDetail(t.Context(), detailTestAccount, "7", true)
	if err != nil || len(with.Seasons) != 2 || provider.detailCalls.Load() != 1 {
		t.Fatalf("SeriesDetail(specials) = %+v, %v, calls %d", with.Seasons, err, provider.detailCalls.Load())
	}
}

func TestTitleDetailRejectsInvalidInputAndUnsupportedProviders(t *testing.T) {
	service, _ := newDetailTestService(t, &detailTestProvider{})
	if _, err := service.MovieDetail(t.Context(), "not-an-id", "1"); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("MovieDetail(bad account) = %v", err)
	}
	if _, err := service.SeriesDetail(t.Context(), detailTestAccount, "0", false); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("SeriesDetail(bad id) = %v", err)
	}
	plain, _ := newDetailTestService(t, &serviceTestProvider{})
	if _, err := plain.MovieDetail(t.Context(), detailTestAccount, "1"); err == nil {
		t.Fatal("MovieDetail accepted a provider without detail support")
	}
}
