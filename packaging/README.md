# Packaging

Farrow has three build layers:

1. `build-dev.sh` emits a development CLI/helper pair for one OS/architecture.
2. GoReleaser emits the four release archives, four native RPM/DEB packages,
   and their SPDX SBOMs from one build identity.
3. `release-local.sh` verifies and assembles that output into one non-published
   release rehearsal.

Release snapshots require the pinned GoReleaser and Syft tools plus `jq`;
snapshot and formal local releases additionally preflight `rpm`, `bsdtar`, and Ruby before
starting the expensive build.

The supported shortcuts are documented in
[`../docs/development.md`](../docs/development.md):

```bash
make build
make cross
make release-check
make release-snapshot
make release-local VERSION="$VERSION"
```

## Scripts

| Script | Purpose |
|---|---|
| `build-dev.sh` | one Darwin/Linux amd64/arm64 CLI/helper pair with injected digest |
| `install.sh` | user-scoped release installer with checksum verification and one atomic current-release pointer |
| `check-toolchain.sh` | verify pinned versions in `toolchain.env` |
| `release-local.sh` | clean tagged local release: GoReleaser nFPM packages + final assembly |
| `build-release.sh` | legacy deterministic development archives; refuses stable versions |
| `verify-release.sh` | strict verification of a development archive set |
| `build-linux-packages.sh` | standalone amd64/arm64 RPM/DEB development path |
| `verify-linux-packages.sh` | payload, dependency, mode, architecture, digest-pairing, and mtime checks |
| `verify-goreleaser.sh` | strict archive/SBOM/mtime verification |
| `goreleaser-companion.sh` | per-target helper build and target-specific digest overlay |
| `goreleaser-package-stage.sh` | exact Linux package payload and BUILD_INFO staging |
| `goreleaser-sbom.sh` | deterministic SPDX namespace and timestamp |
| `goreleaser-package-sbom.sh` | package payload SPDX generation and digest binding |
| `semver.sh` | strict shared SemVer 2.0.0 validator |
| `assemble-release.sh` | combine verified archives and packages into one checksum manifest |
| `render-homebrew.sh` | render `homebrew/farrow.rb.tmpl` for an exact release |
| `test-cosign-roundtrip.sh` | exercise signing/provenance with an ephemeral key |
| `verify-licenses.sh` | verify reachable modules and exact upstream license bytes |

## Package dependencies

Native packages declare hard runtime dependencies; package installation should
leave Quick mode ready for `farrow setup` without a manual dependency list.
Formal packages use nFPM 2.46.3 embedded in the pinned GoReleaser 2.16.0;
standalone nFPM 2.47.0 is used only by the development package path.

| Format/arch | QEMU and firmware | Common |
|---|---|---|
| DEB amd64 | `qemu-system-x86`, `ovmf` | `qemu-utils`, `openssh-client`, `iproute2` |
| DEB arm64 | `qemu-system-arm`, `qemu-efi-aarch64` | `qemu-utils`, `openssh-client`, `iproute2` |
| RPM amd64 | `qemu-kvm`, `edk2-ovmf` | `qemu-img`, `openssh-clients`, `iproute` |
| RPM arm64 | `qemu-kvm`, `edk2-aarch64` | `qemu-img`, `openssh-clients`, `iproute` |

Private networking is a visible setup-time capability, not a package-install
side effect. Setup installs `systemd` on Debian/Ubuntu or `systemd-networkd` on
Fedora when needed, then performs the bounded network transaction. RHEL,
Rocky, AlmaLinux, CentOS, and Oracle Linux packages support Quick only because
Farrow has no NetworkManager private backend yet.

## Output and safety rules

- GoReleaser output is isolated from the historical `dist/` tree. Snapshot and
  local release targets refuse existing output instead of cleaning it.
- Final output is rejected when it resolves inside `.goreleaser-dist` or
  `.goreleaser-companion`, because those roots are always temporary.
- The formal local release requires a clean, fully tracked `v$VERSION` commit
  and never publishes.
- Every verifier takes an absolute output path.
- Reproducible builds derive `SOURCE_DATE_EPOCH` and `FARROW_COMMIT` from the
  exact commit being built.
- The CLI is valid only with the helper whose SHA-256 was injected at build
  time; all archive and package verifiers prove that pairing.

## Directories

| Directory | Contents |
|---|---|
| `homebrew/` | formula template; its existence does not claim a public tap |
| `pigsty/` | `pigsty-vm` wrapper installed on `PATH` |
| `image-pipeline/` | guest-image validation and offline normalization |
