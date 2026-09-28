package db

import (
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

// ActivityStatement returns the engine-specific production activity statement.
func ActivityStatement(driver config.Driver) (string, error) {
	switch driver {
	case config.DriverSQLite:
		return sqlite.ListActivityWatchesStatement(), nil
	case config.DriverPostgres:
		return postgres.ListActivityWatchesStatement(), nil
	default:
		return "", fmt.Errorf("activity statement: unsupported database driver %q", driver)
	}
}
