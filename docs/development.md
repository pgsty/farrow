# Development

## Toolchain

Versions are pinned in `packaging/toolchain.env` and checked with
`./packaging/check-toolchain.sh all`.

| Tool | Version | Used for |
|---|---|---|
| Go | 1.27.0 | building and testing |
| staticcheck | 2026.2 | static analysis |
| govulncheck | 1.7.0 | vulnerability scan |
| GoReleaser | 2.16.0 | release archives, RPM/DEB packages, and SBOM orchestration |
| GoReleaser-embedded nFPM | 2.46.3 | formal RPM/DEB package engine; verified from GoReleaser module metadata |
| standalone nFPM | 2.47.0 | package-development iteration only |
| Syft | 1.51.0 | SPDX SBOM generation |
| jq | host package | release metadata, staging overlays, and verifier queries |
| Cosign | 3.1.3 | signatures and provenance |
| RPM CLI | host package | exact RPM `Requires` and header verification; does not affect artifact bytes |

Updating a pin requires fresh reproducibility evidence from both build runs.

## Local development build

```bash
make build
```

This writes the native developer payload to `bin/`. The CLI/helper entry points
are atomic symlinks into one digest-bound version directory, so an interrupted
rebuild cannot expose a half-old, half-new pair:

| File | Role |
|---|---|
| `farrow` | CLI |
| `farrow-hosts-helper` | narrow `/etc/hosts` publisher paired to the CLI |
| `pigsty-vm` | Pigsty integration wrapper |
| `farrow-m0`, `farrow-net-stage`, `farrow-linux-net-stage`, `farrow-private-m0`, `farrow-private-fd-m0` | development probes; never released |

Always use the Make target or `packaging/build-dev.sh`, even for a quick local
build. The builder compiles the hosts helper first, hashes it, and injects that
digest into the CLI. The CLI verifies the exact root-owned helper before it is
allowed to publish `/etc/hosts`. A bare `go build ./cmd/farrow` omits this
companion pairing.

Development binaries report version `dev` by default and include the resolved
commit and build timestamp. Reproducible test callers may override
`FARROW_VERSION`, `FARROW_COMMIT`, and `FARROW_BUILD_DATE`.

## Cross builds

Each platform target emits a runnable, digest-paired CLI/helper pair without
overwriting another architecture:

```bash
make build-darwin-amd64   # bin/darwin_amd64/{farrow,farrow-hosts-helper}
make build-darwin-arm64   # bin/darwin_arm64/{farrow,farrow-hosts-helper}
make build-linux-amd64    # bin/linux_amd64/{farrow,farrow-hosts-helper}
make build-linux-arm64    # bin/linux_arm64/{farrow,farrow-hosts-helper}
```

Convenience targets:

```bash
make cross          # build all four artifact pairs
make build-cross    # alias for cross
make amd            # alias for build-linux-amd64
make arm            # alias for build-linux-arm64
make cross-check    # compile every Go package for all four targets; no artifacts
```

`cross` is for manually copying/running a CLI and companion on another host.
`cross-check` is the cheaper compatibility gate for CI and code review. Neither
proves HVF/KVM boot behaviour on real hardware.

## CLI architecture boundary

The current command tree still uses Go's standard `flag` package. The output
contract is separate from that parser: text/JSON/YAML rendering, TTY detection,
colour, progress, and verbose diagnostics must remain reusable when the command
tree moves to Cobra.

That migration should remain one coherent change. Cobra will own discovery,
usage, and persistent presentation flags. Viper may own config path,
environment, and precedence, but it must not weaken `internal/config.Load`:
unknown-field rejection, the size limit, multi-document rejection, canonical
paths, and symlink checks remain the project-file boundary. Presentation flags
are runtime-only and must never be written into `farrow.yaml`.

## Test

Run the full gate after a coherent batch of changes:

```bash
make check
```

It runs `test`, `race`, `vet`, `staticcheck`, `vuln`, `cross-check`,
`profile-contract`, `wrapper-test`, the portable `image-pipeline-test`, and
`license-check`. Native guest mutation is deliberately a separate required-input
gate, so `make check` never reports success after silently skipping it.
Use the narrower targets while iterating:

```bash
make test
make race
make vet
make staticcheck
make vuln
make cross-check
make profile-contract
make wrapper-test
make image-pipeline-test
make image-pipeline-native-test  # requires the documented native image inputs
make license-check

PIGSTY_SOURCE=/absolute/path/to/pigsty make pigsty-source-test
```

`pigsty-source-test` is opt-in because it needs a real Pigsty checkout. It
validates all 13 inventory templates against the catalog bindings.

The unit suite is hermetic: no test reaches the network or depends on host VM
state. Inject a seam instead of dialling a real endpoint.

### What source tests do not prove

Generated QEMU argv is not a boot. A fake QMP server is not HVF or KVM. Native
behaviour is established by the bounded end-to-end scripts:

```bash
tests/e2e/quick-product-smoke.sh
tests/e2e/private-full-product-smoke.sh
tests/e2e/private-soak.sh
tests/e2e/quick-smoke.sh
tests/e2e/host-audit.sh
```

The product smokes accept an absolute Farrow binary, an existing mode-0700
data root, and a new evidence root. They remain ownership-bounded. The private
smoke inspects an existing healthy, lease-free network; it never installs or
uninstalls host networking. See [`tests/e2e/README.md`](../tests/e2e/README.md).

Tier-1 hosts are macOS arm64 with HVF and Linux amd64 with KVM. macOS amd64 and
Linux arm64 are compile-and-unit-test targets until native hardware evidence is
recorded.

## Release entry points

The Makefile separates local iteration, GoReleaser snapshots, and a formal
local release.

### Check the release config

```bash
make release-check
# alias: make gr-check
```

This verifies the pinned GoReleaser binary and runs `goreleaser check` without
building an artifact.

### GoReleaser snapshot

```bash
make release-snapshot
# alias: make gr-snapshot
```

The default output is the new repository-root directory
`.goreleaser-snapshot/`. The target refuses an existing path instead of
cleaning it. Select another new direct child of the repository root with:

```bash
make release-snapshot SNAPSHOT_DIST=.snapshot-review
```

Snapshots produce the four Darwin/Linux amd64/arm64 archives, both Linux
architectures in DEB and RPM form, and SPDX SBOMs for every artifact.
GoReleaser runs with parallelism one because concurrent completion can change
member order even when the binaries themselves are deterministic. The target
marks dirty source as `uncommitted` and verifies the complete archive/package
inventory before publishing the new snapshot directory.

Package payload verification requires `rpm` and `bsdtar` in addition to the
pinned GoReleaser/Syft toolchain. On macOS, install them with
`brew install rpm libarchive`; on Debian-family hosts use
`apt install rpm libarchive-tools`.

### Formal local release

```bash
make release-local VERSION="$VERSION"
# alias: make gr-local VERSION="$VERSION"
```

`VERSION` is required. HEAD must be a clean, fully tracked commit whose exact
tag is `v$VERSION`; the command refuses untracked files and existing output.
It runs one GoReleaser build whose `nfpms` definitions create and verify the
architecture-specific RPM/DEB packages from the same deterministic CLI/helper
pair used by the Linux archives. It then assembles one local release tree with
a single checksum manifest, Homebrew formula, SPDX documents, and release
metadata. Install an `rpm` query CLI before this formal verification and make
sure `bsdtar`, `jq`, and Ruby are available. On macOS use `brew install rpm libarchive jq`
and add `$(brew --prefix libarchive)/bin` to `PATH`; on Debian/Ubuntu use
`apt install rpm libarchive-tools jq ruby`.

Default output is `dist/releases/$VERSION`. To select a new path:

```bash
make release-local VERSION="$VERSION" OUTPUT="$PWD/out/farrow-$VERSION"
```

The local release never publishes to GitHub and never signs with production
identity. It is the package/release rehearsal to run before creating a tag
release.

### Development archive builder

The older deterministic development path remains for non-stable versions:

```bash
export FARROW_COMMIT="$(git rev-parse HEAD)"
export SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)"
make release-dev VERSION="${DEV_VERSION}" SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}"
```

`DEV_VERSION` must be a non-stable semantic version accepted by
`packaging/build-release.sh`. The output contains four archives, checksums, a
formula, and development release metadata. It is not the formal local-release
path.

## Linux package dependencies

Formal packages come from `.goreleaser.yaml`. The standalone
`packaging/build-linux-packages.sh` path remains available for iterating on an
individual amd64/arm64 DEB/RPM payload without running the full release.
The package payload installs `/usr/bin/farrow`, the `pigsty-vm` wrapper,
`/opt/farrow/libexec/farrow-hosts-helper`, schemas, documentation, build
metadata, and license material.

Package metadata uses hard dependencies so installing a native package is
enough for Quick setup:

| Package | amd64 | arm64 | Common |
|---|---|---|---|
| DEB | `qemu-system-x86`, `ovmf` | `qemu-system-arm`, `qemu-efi-aarch64` | `qemu-utils`, `openssh-client`, `iproute2` |
| RPM | `qemu-kvm`, `edk2-ovmf` | `qemu-kvm`, `edk2-aarch64` | `qemu-img`, `openssh-clients`, `iproute` |

Private setup additionally installs `systemd` on Debian/Ubuntu or
`systemd-networkd` on Fedora when missing. RHEL-family Private remains
unsupported because the host network is NetworkManager-owned; the RPM still
supports Quick there.

Packages do not enable or alter a host network during package installation.
That mutation belongs to the visible, idempotent `farrow setup meta|full`
transaction.

Each package has an SPDX SBOM. Verification binds `BUILD_INFO.json`, binary
version strings, SBOM timestamps/namespaces, CLI/helper digest pairing, modes,
architecture, and member mtimes to the expected commit and source epoch.

## Reproducibility and signing

Release builds derive `SOURCE_DATE_EPOCH` from the tagged commit. Given the
same commit, versions, and pinned toolchain, two runs must be byte-identical.
All verifier scripts accept absolute artifact paths and never infer a mutable
“latest” directory.

The tag workflow uses GitHub Actions OIDC keyless Cosign. Verify a published
manifest with the exact workflow identity and tag printed by that release; do
not use an open identity regular expression.

The signing mechanism can be exercised locally without production identity:

```bash
./packaging/test-cosign-roundtrip.sh "$PWD/dist/linux-dev/checksums.txt"
```

It creates an encrypted ephemeral key, verifies signature and SLSA provenance,
proves tampering fails, and removes the key directory. Passing it does not
satisfy the production signing gate.

## CI and release policy

`.github/workflows/ci.yml` runs source gates on pushes and pull requests.
`.github/workflows/release.yml` runs on `v*` tags, verifies the tag/commit and
clean-tree invariants, assembles the archive/package set, signs and attests the
final checksum manifest, and creates a draft release.

Production private keys are not stored in this repository. Guest-image
manifest keys and release signing are separate trust domains.

## Conventions

- Strict parsing: unknown fields are errors.
- External programs receive argv slices and deadlines, not shell strings.
- Destructive paths are ownership- and path-bounded; ambiguous state is
  preserved.
- A new dependency needs a documented purpose, pinned tool version when
  applicable, license review, and a standard-library alternative assessment.
