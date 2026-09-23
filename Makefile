# Makefile - single canonical verification entrypoint.
#
# Adapted copy of golang/templates/Makefile for Bloom (ADR 0001).
# Humans and CI run the SAME targets here; do not restate raw command lists
# elsewhere. `make verify` is the ordered safety gate.
#
# Linter config and policy:   golang/quality/linting.md
# CI and release wiring:      golang/operations/ci-and-release.md
#
# Conventions:
#   - Language baseline Go 1.24+; tools are run via `go tool <name>` (added with
#     `go get -tool`).
#   - Pinning golangci-lint and sqlc as `go tool` dependencies raises the
#     module's go.mod `go` directive (the sqlc tool graph requires Go 1.26);
#     `go mod tidy` re-asserts this and the root go.work follows. The 1.24+
#     baseline still describes the service code.
#   - golangci-lint and govulncheck are tool dependencies, never global installs.
#   - -trimpath belongs to release builds only, never the routine `build` target.

# Use bash with strict flags so multi-command recipes fail loudly.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c

.DEFAULT_GOAL := help

.PHONY: help generate generate-check tidy tidy-check fmt fmt-check lint vet test race cover vuln build run verify verify-go web-ci web-verify web-build

help: ## Show this help.
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

tidy: ## Sync go.mod/go.sum in place and verify the module graph.
	go mod tidy
	go mod verify

tidy-check: ## Fail if go mod tidy would change go.mod/go.sum (CI-safe, no writes).
	go mod tidy -diff
	go mod verify

generate: ## Regenerate pinned API clients and sqlc database code.
	go generate ./internal/mediaserver/jellyfin
	go generate ./internal/metadata/tmdb
	go tool sqlc generate

generate-check: ## Fail when committed generated code is stale.
	@before="$$(sha256sum internal/mediaserver/jellyfin/api/zz_generated.openapi.go internal/metadata/tmdb/api/zz_generated.openapi.go internal/db/sqlite/*.go internal/db/postgres/*.go)"; \
	$(MAKE) --no-print-directory generate; \
	after="$$(sha256sum internal/mediaserver/jellyfin/api/zz_generated.openapi.go internal/metadata/tmdb/api/zz_generated.openapi.go internal/db/sqlite/*.go internal/db/postgres/*.go)"; \
	if [ "$$before" != "$$after" ]; then echo "generated code is stale; run: make generate"; exit 1; fi

fmt: ## Format all Go source in place (gofumpt + gci, per .golangci.yml).
	go tool golangci-lint fmt

fmt-check: ## Fail if any file is not formatted (gofumpt + gci; CI-safe, no writes).
	@out="$$(go tool golangci-lint fmt --diff || true)"; \
	if [ -n "$$out" ]; then \
		echo "not formatted (gofumpt/gci):"; echo "$$out"; \
		echo "run: make fmt"; \
		exit 1; \
	fi

lint: ## Run golangci-lint.
	go tool golangci-lint run

vet: ## Run go vet.
	go vet ./...

test: ## Run all tests.
	go test ./...

race: ## Run all tests under the race detector.
	go test -race ./...

cover: ## Write cover.out and print total coverage (atomic mode is race-safe).
	go test -covermode=atomic -coverprofile=cover.out ./...
	go tool cover -func=cover.out | grep '^total:'

vuln: ## Run govulncheck against the module.
	go tool govulncheck ./...

build: ## Compile all packages.
	go build ./...

run: web-build ## Build the SPA, then run the service locally (SQLite by default; needs BLOOM_SECRET_KEY).
	go run ./cmd/bloom

# --- Frontend (web/, ADR 0002) -----------------------------------------------
# The SPA has its own toolchain and its own gate (`npm run verify` in web/).
# These targets are thin callers so humans and CI still have one entrypoint.

web-ci: ## Install frontend dependencies from the lockfile.
	cd web && npm ci --no-audit --no-fund

web-verify: ## Run the frontend gate (format, lint, typecheck, tests, audit, build).
	cd web && npm run verify

web-build: ## Build the SPA into internal/api/web/dist for embedding.
	cd web && npm run build

verify-go: generate-check tidy-check fmt-check lint vet test race vuln build ## Backend gate only.
	@echo "verify-go: OK"

verify: verify-go web-verify ## Full ordered safety gate for both halves (run before every push and in CI).
	@echo "verify: OK"
