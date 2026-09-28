package postgres

// ListActivityWatchesStatement returns the statement executed by ListActivityWatches.
func ListActivityWatchesStatement() string { return listActivityWatches }

// ListUserActivityWatchesStatement returns the statement executed by ListUserActivityWatches.
func ListUserActivityWatchesStatement() string { return listUserActivityWatches }

// ListServerActivityWatchesStatement returns the statement executed by ListServerActivityWatches.
func ListServerActivityWatchesStatement() string { return listServerActivityWatches }

// ListServerUserActivityWatchesStatement returns the statement executed by ListServerUserActivityWatches.
func ListServerUserActivityWatchesStatement() string { return listServerUserActivityWatches }

// ListTimelineWatchesStatement returns the statement executed by ListTimelineWatches.
func ListTimelineWatchesStatement() string { return listTimelineWatches }
