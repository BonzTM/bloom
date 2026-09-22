# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Every operator-visible change (configuration keys, migrations, ports, API
contracts) gets an entry here.

## [Unreleased]

### Added

- Main-branch and tagged-release image publishing with provenance, an SBOM,
  digest-based smoke tests, and automated homelab deployment pull requests.
- Bootstrap of the service: Go HTTP server with `/livez`, `/readyz`, `/metrics`,
  and `GET /api/v1/version`; embedded React UI; SQLite (default) and PostgreSQL
  storage at parity; container image and CI.
- Repository hygiene: Dependabot, pull request template, code owners, security
  policy, and this changelog.

### Fixed

- CI now builds and scans with the same Go toolchain as local and container
  builds (`toolchain go1.27.1` in go.mod).
