// Package buildinfo exposes build-time metadata stamped into the binary via
// -ldflags and surfaced in logs and the GET /api/v1/version endpoint.
//
// The defaults below are intentionally generic so an unstamped `go run` still
// works; release builds override them. The Dockerfile and release pipeline own
// the canonical -X linker flags:
//
//	-X github.com/BonzTM/bloom/internal/buildinfo.Version=v1.2.3
//	-X github.com/BonzTM/bloom/internal/buildinfo.Commit=$(git rev-parse HEAD)
package buildinfo

// Build metadata. These are var (not const) so the linker can override them
// with -ldflags -X at build time; they are never mutated at runtime.
var (
	// Name is the service name.
	Name = "bloom"
	// Version is the release version (semver tag), or "dev" for local builds.
	Version = "dev"
	// Commit is the VCS revision the binary was built from, or "unknown".
	Commit = "unknown"
)
