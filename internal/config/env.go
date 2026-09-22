package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// envReader reads typed values from the environment and accumulates parse
// errors instead of swallowing them. A malformed value (e.g.
// BLOOM_DB_MAX_OPEN_CONNS=abc) is a fail-fast condition per the handbook's
// foundations/configuration.md ("no silent fallback when a value is
// malformed"): each getter records an actionable, key-named error and returns
// the fallback so default seeding can proceed, then Load checks err() and
// aborts before opening listeners. The fallback is used only to keep building
// the flag set; it is never the accepted config when an error was recorded.
type envReader struct {
	errs []error
}

func newEnvReader() *envReader { return &envReader{} }

// err returns the joined parse errors, or nil if every read succeeded.
func (e *envReader) err() error {
	if len(e.errs) == 0 {
		return nil
	}
	return fmt.Errorf("config: invalid environment: %w", errors.Join(e.errs...))
}

func (e *envReader) string(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func (e *envReader) int(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return fallback
	}
	return n
}

func (e *envReader) int64(key string, fallback int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s must be a 64-bit integer, got %q", key, v))
		return fallback
	}
	return n
}

func (e *envReader) duration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s must be a duration like 15s or 1m, got %q", key, v))
		return fallback
	}
	return d
}

func (e *envReader) float64(key string, fallback float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s must be a number, got %q", key, v))
		return fallback
	}
	return f
}

func (e *envReader) bool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s must be a boolean (true/false/1/0), got %q", key, v))
		return fallback
	}
	return b
}
