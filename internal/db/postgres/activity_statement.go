package postgres

// ListActivityWatchesStatement returns the statement executed by ListActivityWatches.
func ListActivityWatchesStatement() string { return listActivityWatches }

// ListTimelineWatchesStatement returns the statement executed by ListTimelineWatches.
func ListTimelineWatchesStatement() string { return listTimelineWatches }
