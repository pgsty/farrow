# Changelog

Notable user-visible changes. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html); everything below
1.0.0 is a pre-1.0 developer release and may break between minor versions.

## [Unreleased]

## [0.1.0]

First public, pre-1.0 developer release of the native Go/QEMU runtime for
Pigsty-compatible local labs.

### Added

- One Pigsty-compatible inventory is both the VM specification and the Pigsty
  deployment inventory; there is no second project format.
- One owner-scoped deployment with fixed-IP additive scale-out on macOS and
  Linux, and explicit drift, recreate, and deletion boundaries.
- QMP plus full process identity, atomic state, transaction journals, and
  manifest-scoped host networking, so lifecycle recovery is fail-closed.
- Signed image catalogs with SHA-256 and qcow2 verification, immutable upstream
  fallback, local imports, and bounded cache pruning.
- Static image repositories: a source-controlled `repo.yaml`, a generated
  schema-3 `catalog.json`, immutable artifact names, and
  `farrow repo scan/build/verify`.
- Image selection by family, channel, exact release, or numeric version prefix,
  so a Pigsty inventory can keep `vm_image: el9` and pick the newest matching
  9.x build with `vm_version`.
- Interrupted image downloads resume from the staged bytes instead of
  restarting, and are bounded by an inactivity watchdog rather than an overall
  deadline, so a slow link finishes rather than failing partway.
- Release assets: four platform archives, amd64/arm64 DEB and RPM packages,
  dependency licenses, SPDX SBOMs, checksums, a Homebrew formula, a user-scoped
  installer, and keyless Sigstore signature and provenance bundles.

### Fixed

- Inventory parsing now rejects duplicate mapping keys, expands YAML merge keys
  with Ansible-compatible precedence, and refuses alias cycles instead of
  silently resolving a different deployment from the same Pigsty inventory.
- Process birth identity is numeric and independent of locale/timezone; matching
  pre-release development state migrates in place, interrupted QEMU starts are
  adopted only from QMP/UUID/pidfile/invocation proof, and failed starts perform
  bounded compensation.
- Selected `status`, `ssh`, `start`, `stop`, and `restart` operations no longer
  fail because an unrelated peer is degraded; whole-deployment operations retain
  full identity auditing.
- The release installer refuses a missing or invalid Sigstore bundle when
  Cosign is available, and explains that pre-1.0 GitHub pre-releases require an
  explicit `FARROW_VERSION`.

[Unreleased]: https://github.com/pgsty/farrow/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/pgsty/farrow/releases/tag/v0.1.0
