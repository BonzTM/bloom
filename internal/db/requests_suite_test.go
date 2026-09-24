package db_test

import (
	"context"
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
	t.Run("requester username batch", func(t *testing.T) {
		testRequesterUsernameBatch(t, accounts, account)
	})
	t.Run("profile round trip", func(t *testing.T) {
		testRequestProfileRoundTrip(t, profileReader, profileWriter, profile)
	})
	t.Run("movie transition and active uniqueness", func(t *testing.T) {
		testRequestTransitionAndUniqueness(t, requestReader, requestWriter, profileWriter, account.ID, profile.ID, now)
	})
	t.Run("fulfilment transitions", func(t *testing.T) {
		testFulfilmentTransitions(t, requestReader, requestWriter, account.ID, profile.ID, now)
	})
	t.Run("dispatch leases are exclusive and reclaimable", func(t *testing.T) {
		testDispatchLeases(t, requestWriter, account.ID, profile.ID, now)
	})
	t.Run("series fulfilment transitions", func(t *testing.T) {
		testSeriesFulfilmentTransitions(t, requestWriter, account.ID, profile.ID, now)
	})
	t.Run("availability claims are fair", func(t *testing.T) {
		testAvailabilityClaimsAreFair(t, requestWriter, account.ID, profile.ID, now)
	})
	t.Run("series season overlap", func(t *testing.T) {
		testSeriesSeasonOverlap(t, requestWriter, account.ID, profile.ID, now)
	})
	t.Run("concurrent series season overlap", func(t *testing.T) {
		testConcurrentSeriesSeasonOverlap(t, requestWriter, account.ID, profile.ID, now)
	})
	t.Run("concurrent quota boundary", func(t *testing.T) {
		testConcurrentRequestQuota(t, accounts, requestWriter, quotaWriter, profile.ID, now)
	})
}

func testRequesterUsernameBatch(t *testing.T, accounts core.AccountStore, account core.Account) {
	t.Helper()
	reader, ok := accounts.(interface {
		UsernamesByAccountIDs(context.Context, []string) (map[string]string, error)
	})
	if !ok {
		t.Fatal("account store does not implement requester username lookup")
	}
	missingID := mustID(t)
	usernames, err := reader.UsernamesByAccountIDs(t.Context(), []string{missingID, account.ID})
	if err != nil {
		t.Fatalf("UsernamesByAccountIDs: %v", err)
	}
	if usernames[account.ID] != account.Username || usernames[missingID] != "" || len(usernames) != 1 {
		t.Fatalf("account usernames = %v, want only %s=%q", usernames, account.ID, account.Username)
	}
}

func testFulfilmentTransitions(
	t *testing.T, reader core.RequestReader, writer core.RequestWriter, accountID, profileID string, now time.Time,
) {
	t.Helper()
	dispatch, ok := writer.(core.RequestDispatchWriter)
	if !ok {
		t.Fatal("request store does not implement RequestDispatchWriter")
	}
	available := requestFixture(t, accountID, profileID, "401", now)
	if err := writer.CreateRequest(t.Context(), available, now, true); err != nil {
		t.Fatalf("create available fixture: %v", err)
	}
	if _, err := writer.TransitionRequest(t.Context(), available.ID, core.RequestPending, core.RequestApproved, accountID, "", now); err != nil {
		t.Fatalf("approve: %v", err)
	}
	snapshot := core.RequestDispatchSnapshot{
		DownloadManagerID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		QualityProfile:    "1", RootFolder: "/movies", Tags: []string{"requests"},
	}
	lease := requestDispatchLease("aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa", now)
	claimed, claimErr := dispatch.ClaimRequestDispatch(t.Context(), available.ID, snapshot, lease, now)
	if claimErr != nil || claimed.DownloadManagerID != snapshot.DownloadManagerID || !slices.Equal(claimed.DispatchTags, snapshot.Tags) {
		t.Fatalf("claim dispatch = %+v, %v", claimed, claimErr)
	}
	processing, dispatchErr := dispatch.RecordRequestDispatch(t.Context(), available.ID, lease.Token, "77", now.Add(time.Second))
	if dispatchErr != nil || processing.Status != core.RequestProcessing || processing.DownloadManagerItemID != "77" {
		t.Fatalf("dispatch = %+v, %v", processing, dispatchErr)
	}
	finished, availableErr := writer.TransitionRequest(t.Context(), available.ID, core.RequestProcessing, core.RequestAvailable, "", "", now.Add(2*time.Second))
	if availableErr != nil || finished.Status != core.RequestAvailable {
		t.Fatalf("available = %+v, %v", finished, availableErr)
	}

	failed := requestFixture(t, accountID, profileID, "402", now)
	if err := writer.CreateRequest(t.Context(), failed, now, true); err != nil {
		t.Fatalf("create failed fixture: %v", err)
	}
	if _, err := writer.TransitionRequest(t.Context(), failed.ID, core.RequestPending, core.RequestApproved, accountID, "", now); err != nil {
		t.Fatalf("approve failure fixture: %v", err)
	}
	failureLease := requestDispatchLease("aaaaaaaa-2222-4222-8222-aaaaaaaaaaaa", now)
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), failed.ID, snapshot, failureLease, now); err != nil {
		t.Fatalf("claim failure fixture: %v", err)
	}
	failed, failureErr := dispatch.FailRequestDispatch(t.Context(), failed.ID, failureLease.Token, "dispatch failed", now.Add(time.Second))
	if failureErr != nil || failed.FailureReason != "dispatch failed" {
		t.Fatalf("failed = %+v, %v", failed, failureErr)
	}
	reapproved, reapproveErr := writer.TransitionRequest(t.Context(), failed.ID, core.RequestFailed, core.RequestApproved, accountID, "retry", now.Add(2*time.Second))
	if reapproveErr != nil || reapproved.Status != core.RequestApproved || reapproved.FailureReason != "" ||
		reapproved.DownloadManagerID != "" || len(reapproved.DispatchTags) != 0 {
		t.Fatalf("reapproved = %+v, %v", reapproved, reapproveErr)
	}
	if _, err := reader.GetRequest(t.Context(), reapproved.ID); err != nil {
		t.Fatalf("reload reapproved request: %v", err)
	}
}

func testDispatchLeases(
	t *testing.T, writer core.RequestWriter, accountID, profileID string, now time.Time,
) {
	t.Helper()
	dispatch := requireRequestDispatchWriter(t, writer)
	request := approvedRequestFixture(t, writer, accountID, profileID, "408", now)
	snapshot := core.RequestDispatchSnapshot{
		DownloadManagerID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		QualityProfile:    "1", RootFolder: "/movies", Tags: []string{},
	}
	first := requestDispatchLease("dddddddd-1111-4111-8111-dddddddddddd", now)
	second := requestDispatchLease("dddddddd-2222-4222-8222-dddddddddddd", now)
	assertOneConcurrentLease(t, dispatch, request.ID, snapshot, first, second, now)
	reclaimRequest := approvedRequestFixture(t, writer, accountID, profileID, "409", now)
	testExpiredLeaseReclaim(t, dispatch, reclaimRequest.ID, snapshot, first, second, now)
}

func assertOneConcurrentLease(
	t *testing.T, dispatch core.RequestDispatchWriter, requestID string, snapshot core.RequestDispatchSnapshot,
	first, second core.RequestDispatchLease, now time.Time,
) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, lease := range []core.RequestDispatchLease{first, second} {
		go func() {
			<-start
			_, err := dispatch.ClaimRequestDispatch(t.Context(), requestID, snapshot, lease, now)
			results <- err
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, core.ErrInvalidTransition) {
			t.Fatalf("concurrent claim error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent claims = %d, want 1", successes)
	}
}

func testExpiredLeaseReclaim(
	t *testing.T, dispatch core.RequestDispatchWriter, requestID string, snapshot core.RequestDispatchSnapshot,
	first, second core.RequestDispatchLease, now time.Time,
) {
	t.Helper()
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), requestID, snapshot, first, now); err != nil {
		t.Fatalf("claim initial lease: %v", err)
	}
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), requestID, snapshot, second, now.Add(time.Second)); !errors.Is(err, core.ErrInvalidTransition) {
		t.Fatalf("claim live lease error = %v, want ErrInvalidTransition", err)
	}
	second.ExpiresAt = first.ExpiresAt.Add(time.Minute)
	active, err := dispatch.ClaimRequestDispatch(t.Context(), requestID, snapshot, second, first.ExpiresAt)
	if err != nil {
		t.Fatalf("reclaim expired lease: %v", err)
	}
	if active.DispatchLeaseToken != second.Token {
		t.Fatalf("reclaimed lease token = %q, want %q", active.DispatchLeaseToken, second.Token)
	}
	if _, err := dispatch.RecordRequestDispatch(t.Context(), requestID, first.Token, "91", first.ExpiresAt); !errors.Is(err, core.ErrInvalidTransition) {
		t.Fatalf("stale success error = %v, want ErrInvalidTransition", err)
	}
	if _, err := dispatch.FailRequestDispatch(t.Context(), requestID, first.Token, "stale", first.ExpiresAt); !errors.Is(err, core.ErrInvalidTransition) {
		t.Fatalf("stale failure error = %v, want ErrInvalidTransition", err)
	}
}

func approvedRequestFixture(
	t *testing.T, writer core.RequestWriter, accountID, profileID, providerID string, now time.Time,
) core.MediaRequest {
	t.Helper()
	request := requestFixture(t, accountID, profileID, providerID, now)
	if err := writer.CreateRequest(t.Context(), request, now, true); err != nil {
		t.Fatalf("create lease request: %v", err)
	}
	if _, err := writer.TransitionRequest(t.Context(), request.ID, core.RequestPending, core.RequestApproved, accountID, "", now); err != nil {
		t.Fatalf("approve lease request: %v", err)
	}
	return request
}

func requestDispatchLease(token string, now time.Time) core.RequestDispatchLease {
	return core.RequestDispatchLease{Token: token, ExpiresAt: now.Add(time.Minute)}
}

func testSeriesFulfilmentTransitions(
	t *testing.T, writer core.RequestWriter, accountID, profileID string, now time.Time,
) {
	t.Helper()
	dispatch := requireRequestDispatchWriter(t, writer)
	series := seriesRequestFixture(t, accountID, profileID, "403", []int{1, 2}, now)
	if err := writer.CreateRequest(t.Context(), series, now, true); err != nil {
		t.Fatalf("create series: %v", err)
	}
	if _, err := writer.TransitionRequest(t.Context(), series.ID, core.RequestPending, core.RequestApproved, accountID, "", now); err != nil {
		t.Fatalf("approve series: %v", err)
	}
	snapshot := core.RequestDispatchSnapshot{
		DownloadManagerID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		QualityProfile:    "2", RootFolder: "/series", Tags: []string{"series"},
	}
	lease := requestDispatchLease("bbbbbbbb-1111-4111-8111-bbbbbbbbbbbb", now)
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), series.ID, snapshot, lease, now); err != nil {
		t.Fatalf("claim series: %v", err)
	}
	processing, err := dispatch.RecordRequestDispatch(t.Context(), series.ID, lease.Token, "88", now.Add(time.Second))
	assertSeasonStatuses(t, processing, core.SeasonProcessing, err)
	available, err := writer.TransitionRequest(t.Context(), series.ID, core.RequestProcessing, core.RequestAvailable, "", "", now.Add(2*time.Second))
	assertSeasonStatuses(t, available, core.SeasonAvailable, err)

	failed := seriesRequestFixture(t, accountID, profileID, "404", []int{3}, now)
	prepareProcessingSeries(t, writer, dispatch, failed, accountID, snapshot, now)
	failed, err = writer.TransitionRequest(t.Context(), failed.ID, core.RequestProcessing, core.RequestFailed, "", "availability failed", now.Add(2*time.Second))
	assertSeasonStatuses(t, failed, core.SeasonFailed, err)
	if failed.FailureReason != "availability failed" {
		t.Fatalf("failure reason = %q", failed.FailureReason)
	}
}

func prepareProcessingSeries(
	t *testing.T, writer core.RequestWriter, dispatch core.RequestDispatchWriter, request core.MediaRequest,
	accountID string, snapshot core.RequestDispatchSnapshot, now time.Time,
) {
	t.Helper()
	if err := writer.CreateRequest(t.Context(), request, now, true); err != nil {
		t.Fatalf("create failure series: %v", err)
	}
	if _, err := writer.TransitionRequest(t.Context(), request.ID, core.RequestPending, core.RequestApproved, accountID, "", now); err != nil {
		t.Fatalf("approve failure series: %v", err)
	}
	lease := requestDispatchLease("bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb", now)
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), request.ID, snapshot, lease, now); err != nil {
		t.Fatalf("claim failure series: %v", err)
	}
	if _, err := dispatch.RecordRequestDispatch(t.Context(), request.ID, lease.Token, "89", now.Add(time.Second)); err != nil {
		t.Fatalf("dispatch failure series: %v", err)
	}
}

func assertSeasonStatuses(t *testing.T, request core.MediaRequest, want core.SeasonStatus, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	for _, season := range request.Seasons {
		if season.Status != want {
			t.Fatalf("season statuses = %+v, want %s", request.Seasons, want)
		}
	}
}

func testAvailabilityClaimsAreFair(
	t *testing.T, writer core.RequestWriter, accountID, profileID string, now time.Time,
) {
	t.Helper()
	dispatch := requireRequestDispatchWriter(t, writer)
	claimer, ok := writer.(core.RequestAvailabilityClaimer)
	if !ok {
		t.Fatal("request store does not implement RequestAvailabilityClaimer")
	}
	ids := make([]string, 0, 3)
	for index, providerID := range []string{"405", "406", "407"} {
		request := requestFixture(t, accountID, profileID, providerID, now.Add(time.Duration(index)*time.Second))
		if err := writer.CreateRequest(t.Context(), request, now, true); err != nil {
			t.Fatalf("create processing request: %v", err)
		}
		if _, err := writer.TransitionRequest(t.Context(), request.ID, core.RequestPending, core.RequestApproved, accountID, "", now); err != nil {
			t.Fatalf("approve processing request: %v", err)
		}
		snapshot := core.RequestDispatchSnapshot{
			DownloadManagerID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
			QualityProfile:    "1", RootFolder: "/movies", Tags: []string{},
		}
		lease := requestDispatchLease(mustID(t), now)
		if _, err := dispatch.ClaimRequestDispatch(t.Context(), request.ID, snapshot, lease, now); err != nil {
			t.Fatalf("claim processing request: %v", err)
		}
		if _, err := dispatch.RecordRequestDispatch(t.Context(), request.ID, lease.Token, providerID, now); err != nil {
			t.Fatalf("record processing request: %v", err)
		}
		ids = append(ids, request.ID)
	}
	checked := make([]string, 0, 4)
	for round := range 2 {
		batch, err := claimer.ClaimRequestsForAvailability(t.Context(), 2, now.Add(time.Duration(round+1)*time.Minute))
		if err != nil {
			t.Fatalf("claim availability: %v", err)
		}
		for _, request := range batch {
			checked = append(checked, request.ID)
			if request.LastAvailabilityCheckAt == nil {
				t.Fatalf("request %s was not stamped", request.ID)
			}
		}
	}
	for _, id := range ids {
		if !slices.Contains(checked, id) {
			t.Fatalf("claims %v do not contain %s", checked, id)
		}
	}
}

func requireRequestDispatchWriter(t *testing.T, writer core.RequestWriter) core.RequestDispatchWriter {
	t.Helper()
	dispatch, ok := writer.(core.RequestDispatchWriter)
	if !ok {
		t.Fatal("request store does not implement RequestDispatchWriter")
	}
	return dispatch
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

func testSeriesSeasonOverlap(t *testing.T, writer core.RequestWriter, accountID, profileID string, now time.Time) {
	t.Helper()
	seasonOne := seriesRequestFixture(t, accountID, profileID, "301", []int{1}, now)
	if err := writer.CreateRequest(t.Context(), seasonOne, now, true); err != nil {
		t.Fatalf("create season 1 request: %v", err)
	}
	seasonTwo := seriesRequestFixture(t, accountID, profileID, "301", []int{2}, now.Add(time.Second))
	if err := writer.CreateRequest(t.Context(), seasonTwo, now, true); err != nil {
		t.Fatalf("create disjoint season 2 request: %v", err)
	}
	overlap := seriesRequestFixture(t, accountID, profileID, "301", []int{2, 3}, now.Add(2*time.Second))
	if err := writer.CreateRequest(t.Context(), overlap, now, true); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("overlapping season request error = %v, want ErrAlreadyExists", err)
	}
	assertTerminalSeriesDoesNotBlock(t, writer, accountID, profileID, core.RequestDeclined, "302", now)
	assertTerminalSeriesDoesNotBlock(t, writer, accountID, profileID, core.RequestFailed, "303", now)
	assertTerminalSeriesDoesNotBlock(t, writer, accountID, profileID, core.RequestAvailable, "305", now)
}

func assertTerminalSeriesDoesNotBlock(
	t *testing.T, writer core.RequestWriter, accountID, profileID string, status core.RequestStatus, providerID string, now time.Time,
) {
	t.Helper()
	first := seriesRequestFixture(t, accountID, profileID, providerID, []int{1}, now)
	if err := writer.CreateRequest(t.Context(), first, now, true); err != nil {
		t.Fatalf("create request before %s: %v", status, err)
	}
	if err := transitionSeriesToTerminal(t, writer, first.ID, accountID, status, now); err != nil {
		t.Fatalf("transition request to %s: %v", status, err)
	}
	second := seriesRequestFixture(t, accountID, profileID, providerID, []int{1}, now.Add(time.Second))
	if err := writer.CreateRequest(t.Context(), second, now, true); err != nil {
		t.Fatalf("request after %s: %v", status, err)
	}
}

func transitionSeriesToTerminal(
	t *testing.T, writer core.RequestWriter, requestID, accountID string, status core.RequestStatus, now time.Time,
) error {
	t.Helper()
	from := core.RequestPending
	if status == core.RequestDeclined {
		_, err := writer.TransitionRequest(t.Context(), requestID, from, status, accountID, "", now)
		return err
	}
	if _, err := writer.TransitionRequest(t.Context(), requestID, from, core.RequestApproved, accountID, "", now); err != nil {
		return err
	}
	dispatch := requireRequestDispatchWriter(t, writer)
	snapshot := core.RequestDispatchSnapshot{
		DownloadManagerID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
		QualityProfile:    "1", RootFolder: "/media", Tags: []string{},
	}
	lease := requestDispatchLease(mustID(t), now)
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), requestID, snapshot, lease, now); err != nil {
		return err
	}
	if status == core.RequestFailed {
		_, err := dispatch.FailRequestDispatch(t.Context(), requestID, lease.Token, "failed", now.Add(time.Second))
		return err
	}
	if _, err := dispatch.RecordRequestDispatch(t.Context(), requestID, lease.Token, "90", now.Add(time.Second)); err != nil {
		return err
	}
	from = core.RequestProcessing
	if status == core.RequestAvailable {
		_, err := writer.TransitionRequest(t.Context(), requestID, from, status, accountID, "", now.Add(2*time.Second))
		return err
	}
	_, err := writer.TransitionRequest(t.Context(), requestID, from, status, accountID, "", now)
	return err
}

func testConcurrentSeriesSeasonOverlap(
	t *testing.T, writer core.RequestWriter, accountID, profileID string, now time.Time,
) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, 2)
	requests := []core.MediaRequest{
		seriesRequestFixture(t, accountID, profileID, "304", []int{1}, now),
		seriesRequestFixture(t, accountID, profileID, "304", []int{1}, now),
	}
	var ready sync.WaitGroup
	ready.Add(len(requests))
	for _, request := range requests {
		go func() {
			ready.Done()
			<-start
			results <- writer.CreateRequest(t.Context(), request, now, true)
		}()
	}
	ready.Wait()
	close(start)
	successes, conflicts := 0, 0
	for range requests {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, core.ErrAlreadyExists):
			conflicts++
		default:
			t.Fatalf("concurrent series request: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("season race successes = %d, conflicts = %d", successes, conflicts)
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

func seriesRequestFixture(
	t *testing.T, accountID, profileID, providerID string, seasonNumbers []int, now time.Time,
) core.MediaRequest {
	t.Helper()
	seasons := make([]core.RequestSeason, 0, len(seasonNumbers))
	for _, number := range seasonNumbers {
		seasons = append(seasons, core.RequestSeason{Number: number, Status: core.SeasonPending})
	}
	return core.MediaRequest{
		ID: mustID(t), Kind: core.MediaKindSeries, Provider: core.MetadataProviderTMDB, ProviderID: providerID,
		Title: "Series " + providerID, Year: 2026, PosterPath: "/series.jpg", RequesterID: accountID,
		ProfileID: profileID, Status: core.RequestPending, Seasons: seasons, CreatedAt: now, UpdatedAt: now,
	}
}
