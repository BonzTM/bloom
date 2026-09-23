package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

const validSessionJSON = `[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","UserName":"alice","DeviceId":"device-1","DeviceName":"TV","Client":"Jellyfin Web","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Pilot","Type":"Episode","SeriesName":"Show","ParentIndexNumber":1,"IndexNumber":2},"PlayState":{"PositionTicks":12340000,"IsPaused":false,"PlayMethod":"DirectStream"}},{"Id":"idle"}]`

func TestListSessionsMapsPlayingItemsAndDropsIdleSessions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("activeWithinSeconds"); got != "60" {
			t.Errorf("activeWithinSeconds = %q, want 60", got)
		}
		_, _ = fmt.Fprint(w, validSessionJSON)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	sessions, err := client.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %+v, %v", sessions, err)
	}
	got := sessions[0]
	if got.Position != 1234*time.Millisecond || got.PlayMethod != core.PlayMethodDirectStream ||
		got.SeriesName != "Show" || got.SeasonNumber == nil || *got.SeasonNumber != 1 ||
		got.EpisodeNumber == nil || *got.EpisodeNumber != 2 {
		t.Fatalf("mapped session = %+v", got)
	}
}

func TestListSessionsRejectsMalformedTicks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PositionTicks":-1}}]`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListSessions(context.Background())
	assertMediaError(t, err, core.MediaServerMalformed)
}

func TestListSessionsRejectsPositiveTickOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PositionTicks":92233720368547759}}]`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListSessions(context.Background())
	assertMediaError(t, err, core.MediaServerMalformed)
}

func TestListSessionsRetriesTransientServerFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListSessions(context.Background())
	assertMediaError(t, err, core.MediaServerUnavailable)
	if calls.Load() != maxAttempts {
		t.Fatalf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

func FuzzSessionMapper(f *testing.F) {
	f.Add([]byte(validSessionJSON))
	f.Add([]byte(`[{"NowPlayingItem":null}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var sessions []jellyfinapi.SessionInfoDto
		if err := json.Unmarshal(data, &sessions); err != nil {
			return
		}
		if _, err := mapSessions(sessions); err != nil {
			return
		}
	})
}
