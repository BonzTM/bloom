# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Every operator-visible change (configuration keys, migrations, ports, API
contracts) gets an entry here.

## [Unreleased]

### Added

- Main-branch and tagged-release image publishing with candidate-first smoke
  tests, source- and signer-bound attestation plus SBOM verification for
  immutable-image reuse, required CI and PostgreSQL gates, pre-promotion
  provenance, ancestry-guarded `main` and version-guarded stable aliases,
  deterministic bounded seven-day cleanup of unpromoted candidates,
  anonymous-pull validation, an SBOM, and one reusable automated homelab
  deployment pull request.
- Bootstrap of the service: Go HTTP server with `/livez`, `/readyz`, `/metrics`,
  and `GET /api/v1/version`; embedded React UI; SQLite (default) and PostgreSQL
  storage at parity; container image and CI.
- Repository hygiene: Dependabot, pull request template, code owners, security
  policy, and this changelog.

### Fixed

- Image publishing skips the artifact attestation steps, with a notice, when
  attestations are unavailable for the repository's plan, and keeps every other
  proof step; a public repository gets signed provenance automatically.
- CI now builds and scans with the same Go toolchain as local and container
  builds (`toolchain go1.27.1` in go.mod).
