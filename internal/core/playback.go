package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	// MaxPlaybackSessions bounds one upstream sessions snapshot.
	MaxPlaybackSessions = 1024
	// MaxWatchPositions bounds retained samples for one watch.
	MaxWatchPositions = 512
	// MaxPlaybackMutations bounds one atomic poll persistence batch.
	MaxPlaybackMutations = 2 * MaxPlaybackSessions
	// MaxRestoredPlaybackWatches covers the largest configured missed-poll lifecycle.
	MaxRestoredPlaybackWatches = 100 * MaxPlaybackSessions
)

// PlayMethod is Bloom's stable playback delivery classification.
type PlayMethod string

const (
	// PlayMethodDirectPlay means the source media is delivered unchanged.
	PlayMethodDirectPlay PlayMethod = "direct_play"
	// PlayMethodDirectStream means the source container is changed without transcoding.
	PlayMethodDirectStream PlayMethod = "direct_stream"
	// PlayMethodTranscode means the source media is transcoded.
	PlayMethodTranscode PlayMethod = "transcode"
	// PlayMethodUnknown preserves an upstream method Bloom does not recognize.
	PlayMethodUnknown PlayMethod = "unknown"
)

// Valid reports whether the method belongs to Bloom's persisted set.
func (m PlayMethod) Valid() bool {
	return m == PlayMethodDirectPlay || m == PlayMethodDirectStream ||
		m == PlayMethodTranscode || m == PlayMethodUnknown
}

// PlaybackSession is one playing item observed in an upstream session snapshot.
type PlaybackSession struct {
	ServerSessionID string
	MediaUserID     string
	Username        string
	DeviceID        string
	DeviceName      string
	Client          string
	ItemID          string
	ItemName        string
	ItemType        string
	SeriesName      string
	SeasonNumber    *int32
	EpisodeNumber   *int32
	Position        time.Duration
	Paused          bool
	PlayMethod      PlayMethod
	LastActivityAt  time.Time
}

// WatchState is the persisted lifecycle state of a watch.
type WatchState string

// WatchSource identifies the observation channel that produced a collected row.
type WatchSource string

const (
	// WatchPlaying identifies an open watch accruing active time.
	WatchPlaying WatchState = "playing"
	// WatchPaused identifies an open watch not accruing active time.
	WatchPaused WatchState = "paused"
	// WatchStopped identifies a closed watch.
	WatchStopped WatchState = "stopped"
	// WatchSourcePoll identifies observations produced by session polling.
	WatchSourcePoll WatchSource = "poll"
	// WatchSourceWebsocket identifies observations produced by a server websocket.
	WatchSourceWebsocket WatchSource = "websocket"
	// WatchSourceWebhook identifies observations produced by a configured webhook.
	WatchSourceWebhook WatchSource = "webhook"
	// WatchSourceImport identifies rows produced by historical import.
	WatchSourceImport WatchSource = "import"
)

// Valid reports whether the source belongs to Bloom's persisted set.
func (s WatchSource) Valid() bool {
	return s == WatchSourcePoll || s == WatchSourceWebsocket ||
		s == WatchSourceWebhook || s == WatchSourceImport
}

// PlaybackKey identifies a watch. SessionID is a secondary discriminator.
type PlaybackKey struct {
	MediaServerID   string
	MediaUserID     string
	DeviceID        string
	ItemID          string
	ServerSessionID string
}

// PlaybackWatch is Bloom's persisted playback entity.
type PlaybackWatch struct {
	ID              string
	MediaServerID   string
	MediaServerName string
	MediaUserID     string
	Username        string
	DeviceID        string
	DeviceName      string
	Client          string
	ServerSessionID string
	ItemID          string
	ItemName        string
	ItemType        string
	SeriesName      string
	SeasonNumber    *int32
	EpisodeNumber   *int32
	PlayMethod      PlayMethod
	State           WatchState
	StartedAt       time.Time
	LastSeenAt      time.Time
	EndedAt         *time.Time
	ActiveTime      time.Duration
	LastPosition    time.Duration
	Source          WatchSource
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Key returns the persisted identity fields used by the collector.
func (w PlaybackWatch) Key() PlaybackKey {
	return PlaybackKey{
		MediaServerID: w.MediaServerID, MediaUserID: w.MediaUserID,
		DeviceID: w.DeviceID, ItemID: w.ItemID, ServerSessionID: w.ServerSessionID,
	}
}

// ActiveTimeAt includes elapsed time since the latest playing observation.
func (w PlaybackWatch) ActiveTimeAt(now time.Time) time.Duration {
	if w.State != WatchPlaying || now.Before(w.LastSeenAt) {
		return w.ActiveTime
	}
	return w.ActiveTime + now.Sub(w.LastSeenAt)
}

// PlaybackPosition is one bounded progress and delivery-method sample.
type PlaybackPosition struct {
	WatchID    string
	ObservedAt time.Time
	Position   time.Duration
	Paused     bool
	PlayMethod PlayMethod
	Source     WatchSource
}

// PlaybackMutation persists one complete watch snapshot and its transition deltas.
type PlaybackMutation struct {
	Watch         PlaybackWatch
	SegmentStart  *time.Time
	SegmentEnd    *time.Time
	SegmentSource WatchSource
	Position      *PlaybackPosition
	CloseReason   string
}

// PlaybackQueryMode selects one bounded store read.
type PlaybackQueryMode uint8

const (
	// PlaybackQueryNow lists open watches across servers.
	PlaybackQueryNow PlaybackQueryMode = iota + 1
	// PlaybackQueryHistory lists cursor-paged closed watches.
	PlaybackQueryHistory
	// PlaybackQueryRecent finds a resumable closed watch by key.
	PlaybackQueryRecent
	// PlaybackQueryRecentServer lists recently closed watches for tracker restore.
	PlaybackQueryRecentServer
)

// PlaybackQuery describes now-playing, history, or collector resume reads.
type PlaybackQuery struct {
	Mode            PlaybackQueryMode
	MediaServerID   string
	PageSize        int
	BeforeStartedAt time.Time
	BeforeID        string
	Key             PlaybackKey
	EndedAfter      time.Time
}

// PlaybackStore is the consumer-owned persistence seam for collection and reads.
type PlaybackStore interface {
	LoadOpenWatches(ctx context.Context, mediaServerID string) ([]PlaybackWatch, error)
	SaveWatches(ctx context.Context, mutations []PlaybackMutation) error
	ListWatches(ctx context.Context, query PlaybackQuery) ([]PlaybackWatch, error)
}

// PlaybackTrackerConfig contains the pure lifecycle thresholds.
type PlaybackTrackerConfig struct {
	MissedPolls  int
	ResumeWindow time.Duration
}

type trackedWatch struct {
	watch  PlaybackWatch
	missed int
}

// PlaybackTracker applies deterministic poll observations without reading a clock.
type PlaybackTracker struct {
	serverID string
	config   PlaybackTrackerConfig
	open     map[PlaybackKey]*trackedWatch
	recent   map[PlaybackKey]PlaybackWatch
}

// NewPlaybackTracker restores open and recent watches for one server.
func NewPlaybackTracker(
	serverID string,
	config PlaybackTrackerConfig,
	openWatches, recentWatches []PlaybackWatch,
) (*PlaybackTracker, error) {
	if serverID == "" || config.MissedPolls < 1 || config.ResumeWindow <= 0 {
		return nil, fmt.Errorf("playback tracker config: %w", ErrInvalidArgument)
	}
	tracker := &PlaybackTracker{
		serverID: serverID, config: config,
		open:   make(map[PlaybackKey]*trackedWatch, len(openWatches)),
		recent: make(map[PlaybackKey]PlaybackWatch, len(recentWatches)),
	}
	if err := tracker.restore(openWatches, recentWatches); err != nil {
		return nil, err
	}
	return tracker, nil
}

func (t *PlaybackTracker) restore(openWatches, recentWatches []PlaybackWatch) error {
	for _, watch := range openWatches {
		if watch.MediaServerID != t.serverID || watch.State == WatchStopped {
			return fmt.Errorf("restore open playback watch: %w", ErrInvalidArgument)
		}
		copy := watch
		t.open[watch.Key()] = &trackedWatch{watch: copy}
	}
	for _, watch := range recentWatches {
		if watch.MediaServerID != t.serverID || watch.State != WatchStopped || watch.EndedAt == nil {
			return fmt.Errorf("restore recent playback watch: %w", ErrInvalidArgument)
		}
		t.recent[watch.Key()] = watch
	}
	return nil
}

// OpenCount returns the number of watches still subject to active polling.
func (t *PlaybackTracker) OpenCount() int { return len(t.open) }

// HasOpen reports whether an observation already belongs to an open watch.
func (t *PlaybackTracker) HasOpen(session PlaybackSession) (bool, error) {
	key, err := t.sessionKey(session)
	if err != nil {
		return false, err
	}
	_, tracked := t.findOpen(key)
	return tracked != nil, nil
}

// RestoreRecent adds one exact-key persistence result as a reopen candidate.
func (t *PlaybackTracker) RestoreRecent(watch PlaybackWatch) error {
	if watch.MediaServerID != t.serverID || watch.State != WatchStopped || watch.EndedAt == nil {
		return fmt.Errorf("restore recent playback watch: %w", ErrInvalidArgument)
	}
	t.recent[watch.Key()] = watch
	return nil
}

// CloseOverflow closes the oldest open watches beyond limit at their last sighting.
func (t *PlaybackTracker) CloseOverflow(limit int) []PlaybackMutation {
	if limit < 0 || len(t.open) <= limit {
		return nil
	}
	keys := make([]PlaybackKey, 0, len(t.open))
	for key := range t.open {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right PlaybackKey) int {
		leftWatch, rightWatch := t.open[left].watch, t.open[right].watch
		if compared := leftWatch.LastSeenAt.Compare(rightWatch.LastSeenAt); compared != 0 {
			return compared
		}
		return compareStrings(leftWatch.ID, rightWatch.ID)
	})
	count := len(keys) - limit
	mutations := make([]PlaybackMutation, 0, count)
	for _, key := range keys[:count] {
		mutations = append(mutations, t.closeWatch(key, t.open[key], "overflow"))
	}
	return mutations
}

func compareStrings(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

// CloseStale closes restored watches whose last sighting predates the startup budget.
func (t *PlaybackTracker) CloseStale(now time.Time, budget time.Duration) []PlaybackMutation {
	now = NormalizeTime(now)
	mutations := make([]PlaybackMutation, 0)
	for key, tracked := range t.open {
		if budget > 0 && now.Sub(tracked.watch.LastSeenAt) <= budget {
			continue
		}
		mutation := t.closeWatch(key, tracked, "startup")
		mutations = append(mutations, mutation)
	}
	return mutations
}

// Observe applies one complete successful sessions snapshot.
func (t *PlaybackTracker) Observe(
	now time.Time,
	source WatchSource,
	sessions []PlaybackSession,
	newID func() (string, error),
) ([]PlaybackMutation, error) {
	if newID == nil || !source.Valid() || len(sessions) > MaxPlaybackSessions {
		return nil, ErrInvalidArgument
	}
	staged := t.clone()
	mutations, err := staged.applyObservation(NormalizeTime(now), source, sessions, newID)
	if err != nil {
		return nil, err
	}
	t.open, t.recent = staged.open, staged.recent
	return mutations, nil
}

func (t *PlaybackTracker) applyObservation(
	now time.Time,
	source WatchSource,
	sessions []PlaybackSession,
	newID func() (string, error),
) ([]PlaybackMutation, error) {
	t.expireRecent(now)
	seen := make(map[PlaybackKey]bool, len(sessions))
	mutations := make([]PlaybackMutation, 0, len(sessions)+len(t.open))
	for _, session := range sessions {
		key, err := t.sessionKey(session)
		if err != nil {
			return nil, err
		}
		if snapshotAlreadySeen(seen, key) {
			continue
		}
		seen[key] = true
		mutation, err := t.observeSession(now, source, key, session, newID)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
	}
	return append(mutations, t.observeMissing(seen)...), nil
}

func (t *PlaybackTracker) clone() *PlaybackTracker {
	clone := &PlaybackTracker{
		serverID: t.serverID, config: t.config,
		open:   make(map[PlaybackKey]*trackedWatch, len(t.open)),
		recent: make(map[PlaybackKey]PlaybackWatch, len(t.recent)),
	}
	for key, tracked := range t.open {
		copy := *tracked
		copy.watch = clonePlaybackWatch(tracked.watch)
		clone.open[key] = &copy
	}
	for key, watch := range t.recent {
		clone.recent[key] = clonePlaybackWatch(watch)
	}
	return clone
}

func clonePlaybackWatch(watch PlaybackWatch) PlaybackWatch {
	watch.SeasonNumber = cloneInt32(watch.SeasonNumber)
	watch.EpisodeNumber = cloneInt32(watch.EpisodeNumber)
	if watch.EndedAt != nil {
		watch.EndedAt = timePointer(*watch.EndedAt)
	}
	return watch
}

func snapshotAlreadySeen(seen map[PlaybackKey]bool, key PlaybackKey) bool {
	if seen[key] {
		return true
	}
	for candidate := range seen {
		if samePrimaryKey(candidate, key) &&
			(candidate.ServerSessionID == "" || key.ServerSessionID == "") {
			return true
		}
	}
	return false
}

func (t *PlaybackTracker) sessionKey(session PlaybackSession) (PlaybackKey, error) {
	if session.MediaUserID == "" || session.DeviceID == "" || session.ItemID == "" ||
		session.Position < 0 || !session.PlayMethod.Valid() {
		return PlaybackKey{}, fmt.Errorf("playback session: %w", ErrInvalidArgument)
	}
	return PlaybackKey{
		MediaServerID: t.serverID, MediaUserID: session.MediaUserID,
		DeviceID: session.DeviceID, ItemID: session.ItemID, ServerSessionID: session.ServerSessionID,
	}, nil
}

func (t *PlaybackTracker) observeSession(
	now time.Time,
	source WatchSource,
	key PlaybackKey,
	session PlaybackSession,
	newID func() (string, error),
) (PlaybackMutation, error) {
	if existingKey, tracked := t.findOpen(key); tracked != nil {
		if existingKey != key {
			delete(t.open, existingKey)
			t.open[key] = tracked
		}
		return updateTrackedWatch(now, source, tracked, session), nil
	}
	if recent, ok := t.findRecent(key, now); ok {
		delete(t.recent, recent.Key())
		tracked := &trackedWatch{watch: recent}
		t.open[key] = tracked
		return reopenTrackedWatch(now, source, tracked, session), nil
	}
	id, err := newID()
	if err != nil {
		return PlaybackMutation{}, fmt.Errorf("create playback watch id: %w", err)
	}
	watch := newPlaybackWatch(id, t.serverID, now, source, session)
	t.open[key] = &trackedWatch{watch: watch}
	return mutationForStart(watch, now, source, session), nil
}

func (t *PlaybackTracker) findOpen(key PlaybackKey) (PlaybackKey, *trackedWatch) {
	if tracked := t.open[key]; tracked != nil {
		return key, tracked
	}
	var newestKey PlaybackKey
	var newest *trackedWatch
	for candidateKey, tracked := range t.open {
		if samePrimaryKey(candidateKey, key) &&
			(candidateKey.ServerSessionID == "" || key.ServerSessionID == "") {
			if newest == nil || watchIsNewer(tracked.watch, newest.watch) {
				newestKey, newest = candidateKey, tracked
			}
		}
	}
	return newestKey, newest
}

func updateTrackedWatch(
	now time.Time,
	source WatchSource,
	tracked *trackedWatch,
	session PlaybackSession,
) PlaybackMutation {
	wasPlaying := tracked.watch.State == WatchPlaying
	if wasPlaying && !now.Before(tracked.watch.LastSeenAt) {
		tracked.watch.ActiveTime += now.Sub(tracked.watch.LastSeenAt)
	}
	applySession(&tracked.watch, session)
	tracked.watch.LastSeenAt = now
	tracked.watch.UpdatedAt = now
	tracked.missed = 0
	mutation := positionMutation(tracked.watch, now, source, session)
	if wasPlaying && session.Paused {
		mutation.SegmentEnd = timePointer(now)
	}
	if !wasPlaying && !session.Paused {
		mutation.SegmentStart = timePointer(now)
		mutation.SegmentSource = source
	}
	return mutation
}

func reopenTrackedWatch(
	now time.Time,
	source WatchSource,
	tracked *trackedWatch,
	session PlaybackSession,
) PlaybackMutation {
	applySession(&tracked.watch, session)
	tracked.watch.LastSeenAt = now
	tracked.watch.EndedAt = nil
	tracked.watch.UpdatedAt = now
	tracked.missed = 0
	mutation := positionMutation(tracked.watch, now, source, session)
	if !session.Paused {
		mutation.SegmentStart = timePointer(now)
		mutation.SegmentSource = source
	}
	return mutation
}

func newPlaybackWatch(
	id, serverID string,
	now time.Time,
	source WatchSource,
	session PlaybackSession,
) PlaybackWatch {
	watch := PlaybackWatch{
		ID: id, MediaServerID: serverID, MediaUserID: session.MediaUserID,
		DeviceID: session.DeviceID, ItemID: session.ItemID, StartedAt: now,
		LastSeenAt: now, Source: source, CreatedAt: now, UpdatedAt: now,
	}
	applySession(&watch, session)
	return watch
}

func applySession(watch *PlaybackWatch, session PlaybackSession) {
	watch.Username = session.Username
	watch.DeviceName = session.DeviceName
	watch.Client = session.Client
	watch.ServerSessionID = session.ServerSessionID
	watch.ItemName = session.ItemName
	watch.ItemType = session.ItemType
	watch.SeriesName = session.SeriesName
	watch.SeasonNumber = cloneInt32(session.SeasonNumber)
	watch.EpisodeNumber = cloneInt32(session.EpisodeNumber)
	watch.PlayMethod = session.PlayMethod
	watch.LastPosition = session.Position
	if session.Paused {
		watch.State = WatchPaused
	} else {
		watch.State = WatchPlaying
	}
}

func mutationForStart(
	watch PlaybackWatch,
	now time.Time,
	source WatchSource,
	session PlaybackSession,
) PlaybackMutation {
	mutation := positionMutation(watch, now, source, session)
	if !session.Paused {
		mutation.SegmentStart = timePointer(now)
		mutation.SegmentSource = source
	}
	return mutation
}

func positionMutation(
	watch PlaybackWatch,
	now time.Time,
	source WatchSource,
	session PlaybackSession,
) PlaybackMutation {
	position := PlaybackPosition{
		WatchID: watch.ID, ObservedAt: now, Position: session.Position,
		Paused: session.Paused, PlayMethod: session.PlayMethod, Source: source,
	}
	return PlaybackMutation{Watch: watch, Position: &position}
}

func (t *PlaybackTracker) observeMissing(seen map[PlaybackKey]bool) []PlaybackMutation {
	mutations := make([]PlaybackMutation, 0)
	for key, tracked := range t.open {
		if seen[key] {
			continue
		}
		tracked.missed++
		if tracked.missed >= t.config.MissedPolls {
			mutations = append(mutations, t.closeWatch(key, tracked, "timeout"))
		}
	}
	return mutations
}

func (t *PlaybackTracker) closeWatch(
	key PlaybackKey,
	tracked *trackedWatch,
	reason string,
) PlaybackMutation {
	endedAt := tracked.watch.LastSeenAt
	wasPlaying := tracked.watch.State == WatchPlaying
	tracked.watch.State = WatchStopped
	tracked.watch.EndedAt = timePointer(endedAt)
	tracked.watch.UpdatedAt = endedAt
	mutation := PlaybackMutation{Watch: tracked.watch, CloseReason: reason}
	if wasPlaying {
		mutation.SegmentEnd = timePointer(endedAt)
	}
	delete(t.open, key)
	t.recent[key] = tracked.watch
	return mutation
}

func (t *PlaybackTracker) findRecent(key PlaybackKey, now time.Time) (PlaybackWatch, bool) {
	if watch, ok := t.recent[key]; ok && recentEnough(watch, now, t.config.ResumeWindow) {
		return watch, true
	}
	var newest PlaybackWatch
	for candidateKey, watch := range t.recent {
		if samePrimaryKey(candidateKey, key) &&
			(candidateKey.ServerSessionID == "" || key.ServerSessionID == "") &&
			recentEnough(watch, now, t.config.ResumeWindow) {
			if newest.EndedAt == nil || watchIsNewer(watch, newest) {
				newest = watch
			}
		}
	}
	return newest, newest.EndedAt != nil
}

func watchIsNewer(left, right PlaybackWatch) bool {
	leftTime, rightTime := left.LastSeenAt, right.LastSeenAt
	if left.EndedAt != nil {
		leftTime = *left.EndedAt
	}
	if right.EndedAt != nil {
		rightTime = *right.EndedAt
	}
	return leftTime.After(rightTime) || (leftTime.Equal(rightTime) && left.ID > right.ID)
}

func (t *PlaybackTracker) expireRecent(now time.Time) {
	for key, watch := range t.recent {
		if !recentEnough(watch, now, t.config.ResumeWindow) {
			delete(t.recent, key)
		}
	}
}

func recentEnough(watch PlaybackWatch, now time.Time, window time.Duration) bool {
	return watch.EndedAt != nil && !now.Before(*watch.EndedAt) && now.Sub(*watch.EndedAt) <= window
}

func samePrimaryKey(left, right PlaybackKey) bool {
	return left.MediaServerID == right.MediaServerID && left.MediaUserID == right.MediaUserID &&
		left.DeviceID == right.DeviceID && left.ItemID == right.ItemID
}

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

// ValidatePlaybackWatch rejects invalid values before they cross into storage.
func ValidatePlaybackWatch(watch PlaybackWatch) error {
	if !ValidID(watch.ID) || !ValidID(watch.MediaServerID) || watch.MediaUserID == "" ||
		watch.DeviceID == "" || watch.ItemID == "" || !watch.PlayMethod.Valid() ||
		!watch.Source.Valid() || watch.ActiveTime < 0 || watch.LastPosition < 0 {
		return ErrInvalidArgument
	}
	if watch.State != WatchPlaying && watch.State != WatchPaused && watch.State != WatchStopped {
		return ErrInvalidArgument
	}
	if watch.State == WatchStopped && watch.EndedAt == nil {
		return ErrInvalidArgument
	}
	if watch.State != WatchStopped && watch.EndedAt != nil {
		return ErrInvalidArgument
	}
	if watch.StartedAt.IsZero() || watch.LastSeenAt.Before(watch.StartedAt) ||
		watch.CreatedAt.IsZero() || watch.UpdatedAt.IsZero() {
		return ErrInvalidArgument
	}
	if watch.EndedAt != nil && (watch.EndedAt.Before(watch.StartedAt) || !watch.EndedAt.Equal(watch.LastSeenAt)) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidatePlaybackMutation validates its nested optional transition values.
func ValidatePlaybackMutation(mutation PlaybackMutation) error {
	if err := ValidatePlaybackWatch(mutation.Watch); err != nil {
		return err
	}
	if mutation.SegmentStart != nil && mutation.SegmentEnd != nil {
		return ErrInvalidArgument
	}
	if (mutation.SegmentStart != nil) != mutation.SegmentSource.Valid() {
		return ErrInvalidArgument
	}
	if mutation.Position == nil {
		return nil
	}
	position := mutation.Position
	if position.WatchID != mutation.Watch.ID || position.ObservedAt.IsZero() ||
		position.Position < 0 || !position.PlayMethod.Valid() || !position.Source.Valid() {
		return ErrInvalidArgument
	}
	return nil
}

// ErrPlaybackStore identifies persistence failures at the playback boundary.
var ErrPlaybackStore = errors.New("playback store")
