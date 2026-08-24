# Development packaging

Release tooling is pinned in `packaging/toolchain.env`; all seven versions can
be checked with `packaging/check-toolchain.sh all`. Updating a tool is an
explicit reviewed change followed by both reproducibility runs and verifiers.

M1 development releases contain the statically linked unprivileged `piglet`
CLI, the narrow `piglet-hosts-helper`, and the Pigsty integration wrapper, plus licenses,
safety/architecture documentation, and the version-1 config schema. The M0
probe/staging binaries are deliberately excluded. Homebrew installs the helper
only as an unprivileged payload; it never executes that user-writable copy as
root. An administrator must explicitly copy it to the fixed root-owned
`/opt/piglet/libexec/piglet-hosts-helper` path before applying a reviewed hosts
plan. The CLI verifies every path component, owner, group, mode, link count,
and file type before sudo execution. Homebrew never becomes a privileged
execution root for `socket_vmnet` installation. Archives, Homebrew, RPM, and
DEB all install the integration wrapper on PATH as `pigsty-vm`.

Build reproducibly by fixing the source timestamp and commit identity:

```bash
SOURCE_DATE_EPOCH=1787486400 \
PIGLET_COMMIT=uncommitted \
./packaging/build-release.sh 0.1.0-dev.20260823 dist/dev

./packaging/verify-release.sh 0.1.0-dev.20260823 dist/dev
```

The builder refuses a non-empty output directory and emits four archives,
`checksums.txt`, a generated Homebrew formula, and `release.json`. The
development manifest explicitly records `signed: false` and `attested: false`.

Re-running with the same source tree, Go toolchain, version, commit, and
`SOURCE_DATE_EPOCH` must produce byte-identical archives and checksums.

## GA-oriented packaging

The checked-in GoReleaser v2 configuration defines the same four archive
targets plus SPDX JSON SBOMs through Syft:

```bash
goreleaser check
```

Local reproducibility snapshots must be committed and tagged, even when using
`--snapshot`; otherwise GoReleaser can expose commit `none` and per-run binary
mtimes. The strict verifier requires every archive member mtime to match the
fixed `SOURCE_DATE_EPOCH` and includes a negative control for unfixed archives.

For an actual committed/tagged revision, set `SOURCE_DATE_EPOCH` to that commit
time before running GoReleaser, then verify its output:

```bash
SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD) \
  goreleaser release --clean --skip=publish --parallelism 1
SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD) \
  ./packaging/verify-goreleaser.sh 1.0.0 "$PWD/dist"
```

An actual GoReleaser release requires a clean committed/tagged Git revision;
the current unborn development tree is intentionally not made into a release
by the packaging scripts.

RPM and DEB packages are produced independently with paired root-owned hosts
helpers and per-package payload SPDX documents. Their metadata weakly
recommends the platform QEMU/qemu-img/OpenSSH/iproute packages but never enables
the private network during install:

```bash
SOURCE_DATE_EPOCH=1787486400 \
PIGLET_COMMIT=uncommitted \
./packaging/build-linux-packages.sh 0.1.0-dev.20260824 \
  dist/linux-dev

./packaging/verify-linux-packages.sh \
  0.1.0-dev.20260824 uncommitted 1787486400 \
  "$PWD/dist/linux-dev"
```

The verifier binds `BUILD_INFO.json`, binary version strings, SPDX timestamps,
and namespaces to the expected commit and source epoch. It also checks every
actual archive member mtime: DEB ar/control/data members use the source epoch;
nFPM's RPM cpio members use its deterministic Unix-epoch canonical value.

For a real tag, `assemble-release.sh` combines the verified GoReleaser and
Linux-package directories with the formula, release metadata, and provenance
predicate, then generates the single final checksum manifest. The tag workflow
signs and attests that manifest before creating a draft release.

Production release signatures use Cosign bundles and an owner-approved OIDC or
KMS identity. No production private key belongs in this repository. See
`docs/RELEASE.md` for verification identity and attestation policy.

The local mechanism can be checked without a production identity. This creates
an encrypted ephemeral development key under the system temporary directory,
verifies signature and SLSA provenance bundles, proves a tampered checksum file
is rejected by both paths, and removes the entire guarded temporary directory
on exit:

```bash
./packaging/test-cosign-roundtrip.sh \
  "$PWD/dist/linux-dev/checksums.txt"
```

The resulting test key and bundle are intentionally not release artifacts.
