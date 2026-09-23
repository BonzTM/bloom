package core

import (
	"errors"
	"testing"
	"time"
)

func TestCheckRequestQuota(t *testing.T) {
	quota := RequestQuota{
		MovieLimit: 2, MoviePeriod: 24 * time.Hour,
		SeasonLimit: 4, SeasonPeriod: 7 * 24 * time.Hour,
	}
	tests := []struct {
		name        string
		kind        MediaKind
		seasonCount int
		usage       RequestQuotaUsage
		want        error
	}{
		{name: "movie allowed", kind: MediaKindMovie, usage: RequestQuotaUsage{Movies: 1}},
		{name: "movie exceeded", kind: MediaKindMovie, usage: RequestQuotaUsage{Movies: 2}, want: ErrQuotaExceeded},
		{name: "seasons allowed", kind: MediaKindSeries, seasonCount: 2, usage: RequestQuotaUsage{Seasons: 2}},
		{name: "seasons exceeded", kind: MediaKindSeries, seasonCount: 2, usage: RequestQuotaUsage{Seasons: 3}, want: ErrQuotaExceeded},
		{name: "movie seasons invalid", kind: MediaKindMovie, seasonCount: 1, want: ErrInvalidArgument},
		{name: "series empty invalid", kind: MediaKindSeries, want: ErrInvalidArgument},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := CheckRequestQuota(quota, testCase.kind, testCase.seasonCount, testCase.usage)
			if !errors.Is(err, testCase.want) || testCase.want == nil && err != nil {
				t.Fatalf("CheckRequestQuota error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestTransitionPermissionClosedTable(t *testing.T) {
	permission, err := TransitionPermission(RequestPending, RequestApproved)
	if err != nil || permission != PermissionRequestsApprove {
		t.Fatalf("pending to approved = %q, %v", permission, err)
	}
	if _, err := TransitionPermission(RequestDeclined, RequestApproved); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("declined to approved error = %v", err)
	}
}

func TestRequestOwnershipPolicy(t *testing.T) {
	const owner = "11111111-1111-4111-8111-111111111111"
	const other = "22222222-2222-4222-8222-222222222222"
	request := MediaRequest{RequesterID: owner}
	if err := AuthorizeRequestRead(owner, false, request); err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if err := AuthorizeRequestRead(other, true, request); err != nil {
		t.Fatalf("approver read: %v", err)
	}
	if err := AuthorizeRequestRead(other, false, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrelated read error = %v", err)
	}
	filter, err := ScopeRequestList(owner, false, RequestListFilter{RequesterID: other})
	if err != nil || filter.RequesterID != owner {
		t.Fatalf("scoped filter = %+v, %v", filter, err)
	}
}
