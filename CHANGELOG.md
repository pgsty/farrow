# Changelog

Notable user-visible changes. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html); everything below
1.0.0 is a pre-1.0 developer release and may break between minor versions.

## [Unreleased]

### Fixed

- `SIGINT`/`SIGTERM` now cancel long-running commands through Cobra's context,
  stop progress output, release normal deferred resources, and exit 130 with
  one structured cancellation result; Ctrl-C inside SSH remains remote.
- `farrow reload` is one engine operation: definition drift and removed
  nodes are refused before any node stops, and `--json`/`--yaml` emit a
  single document instead of one per phase.
- `farrow ssh` and `farrow exec` refuse a misspelled node before `--` instead
  of running it as a command on the control node.
- Unknown or repeated node selectors are usage errors (exit 2) reported before
  host preflight or a confirmation prompt, not runtime errors after them.
- `farrow destroy` asks for the same `destroy` token whether or not nodes are
  selected, and prints the exact scope (nodes, disks, keys, state) first.
- Refusing or leaving `farrow setup` confirmation unanswered, and mismatching
  the `destroy`/`recreate` token, now return one cancellation result and exit
  130; only an answered setup prompt defaults to yes. Running `setup` without
  `--yes` on a pipe is a usage error (exit 2), like `destroy` without
  `--force`.
- An interrupted `up`, `recreate`, or `destroy` still appends its event to
  `events.jsonl`, so the audit trail shows what the signal cut short.
- `farrow logs --source events` rejects a node argument instead of silently
  returning the deployment-wide log under that name.
- Combined verbose shorthands (`-vv`, `-nv`) enable diagnostics.
- Structured failure payloads keep the real reason for usage errors that were
  previously written straight to stderr.
- Verbose diagnostics redact credentials and query strings from source URLs.
- `farrow-hosts-helper --help` exits 0; its non-root and positional-argument
  tests no longer pass for the wrong reason.

### Changed

- Inventory validation and migration diagnostics now consistently call the
  single owner-scoped state a deployment rather than a project.
- Ambiguous `farrow ssh`/`farrow exec` invocations without `--` now warn once
  when the first token is treated as a remote command; the 0.1.x grammar and
  exit status remain unchanged.
- Installer and development builds retain at most three verified release
  directories by default; set `FARROW_INSTALL_KEEP=N` to choose another bound
  or `FARROW_INSTALL_KEEP=0` to disable pruning.
- The presentation environment variables `FARROW_OUTPUT` and `FARROW_VERBOSE`
  are read directly; Viper and its nine transitive modules are gone from the
  dependency graph and the shipped license inventory.
- Retired the unreachable user-NAT setup branches and four dead APIs.

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
- The signed public repository at `https://repo.pigsty.cc/farrow` is the default
  catalog and artifact mirror; `--repo` and `FARROW_REPO` remain explicit
  overrides, and immutable distribution upstreams remain the fallback.
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
