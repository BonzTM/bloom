package core

import (
	"context"
	"time"
)

const (
	// MaxActivityPageSize bounds public activity and timeline pages.
	MaxActivityPageSize = 100
	// MaxTimelineWatchFetch bounds the watches grouped for one timeline page.
	MaxTimelineWatchFetch = 500
	// MaxActivitySearchBytes bounds a case-insensitive title search.
	MaxActivitySearchBytes = 128
	// DefaultTimelineGap joins adjacent plays within six hours.
	DefaultTimelineGap = 6 * time.Hour
	// MaxTimelineGap bounds caller-selected timeline grouping.
	MaxTimelineGap = 7 * 24 * time.Hour
)

// ActivityCursor is the stable started-at/id position used by activity reads.
type ActivityCursor struct {
	StartedAt time.Time
	ID        string
}

// ActivityQuery is one validated, keyset-paged watch search.
type ActivityQuery struct {
	MediaServerID, MediaUserID, LibraryID string
	ItemType, Client, DeviceID            string
	PlayMethod                            PlayMethod
	Source                                WatchSource
	ImportSource                          ImportSource
	StartedAfter, StartedBefore           *time.Time
	Search                                string
	Before                                *ActivityCursor
	Limit                                 int
}

// ActivityStore reads visible watches for activity and timeline pages.
type ActivityStore interface {
	ListActivity(context.Context, ActivityQuery) ([]PlaybackWatch, error)
	ListTimelineWatches(context.Context, TimelineWatchQuery) ([]PlaybackWatch, error)
}

// TimelineWatchQuery selects the bounded raw watch window used for grouping.
type TimelineWatchQuery struct {
	MediaServerID string
	MediaUserID   string
	Before        *ActivityCursor
	Limit         int
}

// TimelineEntry groups adjacent plays of one item.
type TimelineEntry struct {
	MediaServerID  string
	MediaUserID    string
	Username       string
	ItemID         string
	ItemName       string
	ItemType       string
	SeriesID       string
	SeriesName     string
	LibraryID      string
	LibraryName    string
	FirstStartedAt time.Time
	LastEndedAt    *time.Time
	PlayCount      int
	ActiveTime     time.Duration
}

// TimelinePage is a bounded grouped result and the last consumed watch.
type TimelinePage struct {
	Items     []TimelineEntry
	Next      *ActivityCursor
	Truncated bool
}

// GroupTimeline groups a newest-first watch window without inspecting more than 500 watches.
func GroupTimeline(watches []PlaybackWatch, pageSize int, gap time.Duration) TimelinePage {
	page := TimelinePage{Items: make([]TimelineEntry, 0, pageSize)}
	if pageSize < 1 || pageSize > MaxActivityPageSize || gap <= 0 || gap > MaxTimelineGap ||
		len(watches) > MaxTimelineWatchFetch {
		return page
	}
	consumed := 0
	for _, watch := range watches {
		if !appendTimelineWatch(&page.Items, watch, pageSize, gap) {
			break
		}
		consumed++
	}
	page.Truncated = consumed < len(watches) || len(watches) == MaxTimelineWatchFetch
	if page.Truncated && consumed > 0 {
		last := watches[consumed-1]
		page.Next = &ActivityCursor{StartedAt: last.StartedAt, ID: last.ID}
	}
	return page
}

func appendTimelineWatch(entries *[]TimelineEntry, watch PlaybackWatch, limit int, gap time.Duration) bool {
	if len(*entries) > 0 {
		last := &(*entries)[len(*entries)-1]
		if last.ItemID == watch.ItemID && last.FirstStartedAt.Sub(watch.StartedAt) <= gap {
			last.FirstStartedAt = watch.StartedAt
			last.PlayCount++
			last.ActiveTime += watch.ActiveTime
			return true
		}
	}
	if len(*entries) == limit {
		return false
	}
	*entries = append(*entries, timelineEntryFromWatch(watch))
	return true
}

func timelineEntryFromWatch(watch PlaybackWatch) TimelineEntry {
	return TimelineEntry{
		MediaServerID: watch.MediaServerID, MediaUserID: watch.MediaUserID, Username: watch.Username,
		ItemID: watch.ItemID, ItemName: watch.ItemName, ItemType: watch.ItemType,
		SeriesID: watch.SeriesID, SeriesName: watch.SeriesName,
		LibraryID: watch.LibraryID, LibraryName: watch.LibraryName,
		FirstStartedAt: watch.StartedAt, LastEndedAt: watch.EndedAt,
		PlayCount: 1, ActiveTime: watch.ActiveTime,
	}
}
