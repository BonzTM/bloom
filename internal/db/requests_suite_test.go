package db_test

import (
	"database/sql"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

func runRequestEngineTests(t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore) {
	t.Helper()
	profileReader, profileWriter, err := db.NewRequestProfileStores(pool, driver)
	if err != nil {
		t.Fatalf("NewRequestProfileStores: %v", err)
	}
	requestReader, requestWriter, _, quotaWriter, _, err := db.NewRequestStores(pool, driver)
	if err != nil {
		t.Fatalf("NewRequestStores: %v", err)
	}
	now := core.NormalizeTime(time.Date(2026, 9, 23, 18, 0, 0, 123456789, time.UTC))
	account := requestTestAccount(t, accounts, now, "requester")
	profile := requestTestProfile(t, profileWriter, now)
	t.Run("profile round trip", func(t *testing.T) {
		testRequestProfileRoundTrip(t, profileReader, profileWriter, profile)
	})
	t.Run("request transition and active uniqueness", func(t *testing.T) {
		testRequestTransitionAndUniqueness(t, requestReader, requestWriter, profileWriter, account.ID, profile.ID, now)
	})
	t.Run("concurrent quota boundary", func(t *testing.T) {
		testConcurrentRequestQuota(t, accounts, requestWriter, quotaWriter, profile.ID, now)
	})
}

func requestTestAccount(t *testing.T, accounts core.AccountStore, now time.Time, prefix string) core.Account {
	t.Helper()
	account := core.Account{ID: mustID(t), Username: prefix + "-" + mustID(t), CreatedAt: now}
	if err := accounts.CreateAccount(t.Context(), account); err != nil {
		t.Fatalf("create request test account: %v", err)
	}
	return account
}

func requestTestProfile(t *testing.T, writer core.RequestProfileWriter, now time.Time) core.RequestProfile {
	t.Helper()
	profile := core.RequestProfile{
		ID: mustID(t), Name: "Default requests", Kinds: []core.MediaKind{core.MediaKindMovie, core.MediaKindSeries},
		DownloadManagerKind: "placeholder", DownloadManagerInstance: "future", QualityProfile: "Any",
		RootFolder: "/media", Tags: []string{"requests", "standard"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := writer.CreateRequestProfile(t.Context(), profile); err != nil {
		t.Fatalf("CreateRequestProfile: %v", err)
	}
	return profile
}

func testRequestProfileRoundTrip(t *testing.T, reader core.RequestProfileReader, writer core.RequestProfileWriter, profile core.RequestProfile) {
	t.Helper()
	got, err := reader.GetRequestProfile(t.Context(), profile.ID)
	if err != nil || got.Name != profile.Name || !slices.Equal(got.Tags, profile.Tags) || !slices.Equal(got.Kinds, profile.Kinds) {
		t.Fatalf("GetRequestProfile = %+v, %v", got, err)
	}
	got.Name, got.Tags = "Updated requests", []string{"updated"}
	if updateErr := writer.UpdateRequestProfile(t.Context(), got); updateErr != nil {
		t.Fatalf("UpdateRequestProfile: %v", updateErr)
	}
	listed, err := reader.ListRequestProfiles(t.Context(), "", 10)
	if err != nil || len(listed) != 1 || listed[0].Name != got.Name {
		t.Fatalf("ListRequestProfiles = %+v, %v", listed, err)
	}
}

func testRequestTransitionAndUniqueness(
	t *testing.T,
	reader core.RequestReader,
	writer core.RequestWriter,
	profiles core.RequestProfileWriter,
	accountID, profileID string,
	now time.Time,
) {
	t.Helper()
	request := requestFixture(t, accountID, profileID, "101", now)
	if err := writer.CreateRequest(t.Context(), request, now, true); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	duplicate := requestFixture(t, accountID, profileID, "101", now.Add(time.Second))
	if err := writer.CreateRequest(t.Context(), duplicate, now, true); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("active duplicate error = %v", err)
	}
	decided, err := writer.TransitionRequest(t.Context(), request.ID, core.RequestPending, core.RequestDeclined, accountID, "not now", now.Add(time.Minute))
	if err != nil || decided.Status != core.RequestDeclined || decided.DecisionReason != "not now" {
		t.Fatalf("TransitionRequest = %+v, %v", decided, err)
	}
	if createErr := writer.CreateRequest(t.Context(), duplicate, now, true); createErr != nil {
		t.Fatalf("re-request after decline: %v", createErr)
	}
	loaded, err := reader.GetRequest(t.Context(), duplicate.ID)
	if err != nil || loaded.Title != duplicate.Title {
		t.Fatalf("GetRequest = %+v, %v", loaded, err)
	}
	if err := profiles.DeleteRequestProfile(t.Context(), profileID); !errors.Is(err, core.ErrProfileInUse) {
		t.Fatalf("DeleteRequestProfile referenced error = %v", err)
	}
}

func testConcurrentRequestQuota(
	t *testing.T,
	accounts core.AccountStore,
	writer core.RequestWriter,
	quotas core.RequestQuotaWriter,
	profileID string,
	now time.Time,
) {
	t.Helper()
	account := requestTestAccount(t, accounts, now, "quota")
	quota := core.AccountRequestQuota{AccountID: account.ID, Quota: core.RequestQuota{MovieLimit: 1, MoviePeriod: 24 * time.Hour}}
	if err := quotas.SetAccountRequestQuota(t.Context(), quota); err != nil {
		t.Fatalf("SetAccountRequestQuota: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	requests := []core.MediaRequest{
		requestFixture(t, account.ID, profileID, "200", now),
		requestFixture(t, account.ID, profileID, "201", now),
	}
	var ready sync.WaitGroup
	ready.Add(2)
	for _, request := range requests {
		go func() {
			ready.Done()
			<-start
			results <- writer.CreateRequest(t.Context(), request, now, false)
		}()
	}
	ready.Wait()
	close(start)
	successes, exceeded := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, core.ErrQuotaExceeded):
			exceeded++
		default:
			t.Fatalf("concurrent CreateRequest: %v", err)
		}
	}
	if successes != 1 || exceeded != 1 {
		t.Fatalf("quota race successes = %d, exceeded = %d", successes, exceeded)
	}
}

func requestFixture(t *testing.T, accountID, profileID, providerID string, now time.Time) core.MediaRequest {
	t.Helper()
	return core.MediaRequest{
		ID: mustID(t), Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: providerID,
		Title: "Request " + providerID, Year: 2026, PosterPath: "/poster.jpg", RequesterID: accountID,
		ProfileID: profileID, Status: core.RequestPending, CreatedAt: now, UpdatedAt: now,
	}
}
