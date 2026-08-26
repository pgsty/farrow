# Packaging

Build and verification scripts for releases. Procedures, toolchain pins and
signing policy are documented in [`../docs/development.md`](../docs/development.md).

| Script | Purpose |
|---|---|
| `check-toolchain.sh` | verify every pinned tool version in `toolchain.env` |
| `build-release.sh` | development archives, Homebrew formula, `release.json`; refuses stable versions |
| `verify-release.sh` | strict check of a development archive set |
| `build-linux-packages.sh` | RPM and DEB payloads with per-package SBOMs |
| `verify-linux-packages.sh` | payload, mode, architecture, digest pairing and mtime checks |
| `verify-goreleaser.sh` | strict check of GoReleaser output, including reproducible mtimes |
| `goreleaser-companion.sh` | per-target hosts-helper build and digest overlay hooks |
| `goreleaser-sbom.sh` | deterministic SPDX document namespace and timestamp |
| `assemble-release.sh` | combine verified outputs into one final checksum manifest |
| `render-homebrew.sh` | generate the formula from `homebrew/farrow.rb.tmpl` |
| `test-cosign-roundtrip.sh` | exercise signing and provenance with an ephemeral key |
| `verify-licenses.sh` | reachable module set and exact upstream license bytes |

| Directory | Contents |
|---|---|
| `homebrew/` | formula template |
| `pigsty/` | the `pigsty-vm` wrapper installed on `PATH` |
| `image-pipeline/` | guest image validation and offline normalization |

All verifier scripts take **absolute** output paths. Reproducible builds
require `SOURCE_DATE_EPOCH` and `FARROW_COMMIT` to be set from the exact
commit being built.
