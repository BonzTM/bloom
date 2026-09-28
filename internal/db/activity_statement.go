package db

import (
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

// ActivityStatement returns the engine-specific production activity statement for one indexed filter shape.
func ActivityStatement(driver config.Driver, serverID, userID string) (string, error) {
	switch driver {
	case config.DriverSQLite:
		return sqliteActivityStatement(serverID, userID), nil
	case config.DriverPostgres:
		return postgresActivityStatement(serverID, userID), nil
	default:
		return "", fmt.Errorf("activity statement: unsupported database driver %q", driver)
	}
}

func sqliteActivityStatement(serverID, userID string) string {
	switch {
	case serverID != "" && userID != "":
		return sqlite.ListServerUserActivityWatchesStatement()
	case serverID != "":
		return sqlite.ListServerActivityWatchesStatement()
	case userID != "":
		return sqlite.ListUserActivityWatchesStatement()
	default:
		return sqlite.ListActivityWatchesStatement()
	}
}

func postgresActivityStatement(serverID, userID string) string {
	switch {
	case serverID != "" && userID != "":
		return postgres.ListServerUserActivityWatchesStatement()
	case serverID != "":
		return postgres.ListServerActivityWatchesStatement()
	case userID != "":
		return postgres.ListUserActivityWatchesStatement()
	default:
		return postgres.ListActivityWatchesStatement()
	}
}

// TimelineStatement returns the engine-specific production timeline statement.
func TimelineStatement(driver config.Driver) (string, error) {
	switch driver {
	case config.DriverSQLite:
		return sqlite.ListTimelineWatchesStatement(), nil
	case config.DriverPostgres:
		return postgres.ListTimelineWatchesStatement(), nil
	default:
		return "", fmt.Errorf("timeline statement: unsupported database driver %q", driver)
	}
}
