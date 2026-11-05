# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- MIT `LICENSE` file; the project was previously unlicensed.
- This changelog.

### Changed

- Minimum Go version raised to 1.26; CI now tests against Go 1.26 and 1.27.
- Replaced the archived `gopkg.in/yaml.v3` dependency with its maintained
  continuation `go.yaml.in/yaml/v3` (v3.0.6). No behaviour change.
- Bumped GitHub Actions: `actions/checkout` to v7.0.1 and
  `actions/setup-go` to v7.0.0.

## [0.1.0] - 2026-07-26

### Added

- Initial release: the `credchase` CLI with the facility requirement matrix
  and schema linter, the missing/expiring engine with a red/amber/green
  board, fixed escalation windows backed by an idempotent chase ledger, and a
  CSV audit export.

[Unreleased]: https://github.com/moveeeax/credential-chase/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/moveeeax/credential-chase/releases/tag/v0.1.0
