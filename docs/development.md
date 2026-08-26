# Development

## Toolchain

Versions are pinned in `packaging/toolchain.env` and checked with
`./packaging/check-toolchain.sh all`.

| Tool | Version | Used for |
|---|---|---|
| Go | 1.27.0 | building and testing |
| staticcheck | 2026.2 | static analysis |
| govulncheck | 1.7.0 | vulnerability scan |
| GoReleaser | 2.16.0 | release archives and SBOMs |
| nFPM | 2.47.0 | RPM and DEB packages |
| Syft | 1.51.0 | SPDX SBOM generation |
| Cosign | 3.1.3 | signatures and provenance |

Updating any of them is an explicit change that requires fresh reproducibility
evidence from both build runs.

## Build

```bash
make build
```

Produces eight binaries in `bin/`:

| Binary | Role |
|---|---|
| `farrow` | the CLI — the only binary users need |
| `farrow-hosts-helper` | narrow root-owned `/etc/hosts` publisher |
| `pigsty-vm` | Pigsty integration wrapper, installed on `PATH` |
| `farrow-m0`, `farrow-net-stage`, `farrow-linux-net-stage`, `farrow-private-m0`, `farrow-private-fd-m0` | development probes, excluded from releases |

**Always build through `make`.** The target compiles the hosts helper first,
hashes it, and injects that digest into the CLI with
`-ldflags -X .../internal/hostconfig.ExpectedHelperSHA256=<sha>`. The CLI
verifies that digest before invoking the helper through sudo. A bare
`go build ./cmd/farrow` leaves the variable empty, which skips the pairing
check entirely.

`make build` does not inject version metadata, so `farrow version` reports
`dev`. Only the release scripts set `internal/version.Version`, `.Commit` and
`.Date`.

## CLI architecture boundary

The current command tree still uses Go's standard `flag` package. The output
contract is intentionally separate from that parser: text/JSON/YAML rendering,
TTY detection, colour, progress and verbose diagnostics must remain reusable
when the command tree moves to Cobra.

That migration is deferred from the output work and should be one coherent
change rather than a command-by-command mixture. Cobra will own command
discovery, usage and persistent presentation flags. Viper may own config path,
environment and precedence, but it must not weaken `internal/config.Load`:
unknown-field rejection, the size limit, multi-document rejection, canonical
paths and symlink checks remain the authoritative project-file boundary.
Presentation flags are runtime-only and must never be written into
`farrow.yaml`.

## Test

```bash
make check
```

runs, in order: `test`, `race`, `vet`, `staticcheck`, `vuln`, `cross`,
`profile-contract`, `wrapper-test`, `image-pipeline-test`, `license-check`.

Individually:

```bash
make test              # go test ./...
make race              # go test -race ./...
make vet
make staticcheck
make vuln              # govulncheck ./...
make cross             # darwin/linux × amd64/arm64 compile
make profile-contract  # 13 profiles / 85 nodes catalog contract
make wrapper-test      # pigsty-vm wrapper behaviour
make image-pipeline-test
make license-check     # module set and exact upstream license bytes

PIGSTY_SOURCE=/absolute/path/to/pigsty make pigsty-source-test
```

`pigsty-source-test` is opt-in because it needs a real Pigsty checkout. It
validates all 13 inventory templates against the catalog bindings — exact
semantic token and host counts, no-proxy and tuning behaviour, sequential
Makefile expansion, and source-release exclusion.

The unit suite is hermetic: no test reaches the network or depends on host VM
state. Keep it that way — inject the seam rather than dialling a real address.

### What the tests do not prove

Generated QEMU argv is not a boot. A fake QMP server is not HVF or KVM. A
cross-compile is a compile. Real VM behaviour is only established by running
the end-to-end scripts on a native host:

```bash
tests/e2e/quick-product-smoke.sh         # Quick product plus one explicit read-write 9p share
tests/e2e/private-full-product-smoke.sh  # four-node full plus one read-only share; network already installed
tests/e2e/private-soak.sh                # stop/start soak of an existing full project
tests/e2e/quick-smoke.sh                 # historical farrow-m0 vertical-slice probe
tests/e2e/host-audit.sh                  # read-only host network snapshot
```

Both product smoke scripts take an absolute `farrow` binary, an existing
mode-0700 data root, and a new absolute evidence root. They use only a project
created below that evidence root and re-prove marker ownership before trap
cleanup. In addition to their lifecycle and network assertions, Quick runs one
bounded sudo provision and host↔guest share round-trip, while `full` runs the
same provision across four nodes with bounded parallelism and checks one
read-only share on the control node. The private product smoke only inspects and
preflights an existing healthy, lease-free network; it never installs or
uninstalls one. See
[`tests/e2e/README.md`](../tests/e2e/README.md) for the exact scope and safety
boundary of each script.

Tier‑1 hosts are macOS arm64 with HVF and Linux amd64 with KVM. macOS amd64 and
Linux arm64 are compile-and-unit-test only.

## CI

`.github/workflows/ci.yml` runs the source gates on every push and pull
request. `.github/workflows/release.yml` triggers on `v*` tags only.

## Packaging

Two paths. `packaging/build-release.sh` produces development builds and refuses
a stable version string. GoReleaser is the authority for real releases.

### Development archives

```bash
export FARROW_COMMIT="$(git rev-parse HEAD)"
export SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)"

./packaging/build-release.sh 0.1.3-next "$PWD/dist/dev"
./packaging/verify-release.sh 0.1.3-next "$PWD/dist/dev"
```

Or through make:

```bash
make release-dev VERSION=0.1.3-next SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD)
```

Output is four archives, `checksums.txt`, a generated Homebrew formula, and
`release.json` — which records `"signed": false`, `"attested": false`,
`"channel": "development"`. The builder refuses a non-empty output directory.

Rebuilding with the same source, toolchain, version, commit and
`SOURCE_DATE_EPOCH` must produce byte-identical output. Verify it:

```bash
./packaging/build-release.sh 0.1.3-next "$PWD/dist/a"
./packaging/build-release.sh 0.1.3-next "$PWD/dist/b"
diff -r "$PWD/dist/a" "$PWD/dist/b"
```

Both verifier scripts require **absolute** output paths.

### Linux packages

```bash
./packaging/build-linux-packages.sh 0.1.3-next "$PWD/dist/linux-dev"
./packaging/verify-linux-packages.sh \
  0.1.3-next "${FARROW_COMMIT}" "${SOURCE_DATE_EPOCH}" "$PWD/dist/linux-dev"
```

Packages install `/usr/bin/farrow`, `/opt/farrow/libexec/farrow-hosts-helper`,
`/usr/share/farrow/schemas/farrow-v1.schema.json` and the `pigsty-vm` wrapper,
all root-owned. Metadata *recommends* rather than requires QEMU, `qemu-img`,
OpenSSH and iproute, because names and capabilities differ per distribution —
`farrow doctor` is the real capability check. Nothing about the private network
is installed or enabled.

The verifier binds `BUILD_INFO.json`, binary version strings, SPDX timestamps
and namespaces to the expected commit and epoch, proves the CLI/helper digest
pairing, and checks every archive member mtime.

### Release archives

```bash
goreleaser check

SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD) \
  goreleaser release --clean --skip=publish --parallelism 1

SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD) \
  ./packaging/verify-goreleaser.sh <version> "$PWD/dist"
```

`--parallelism 1` is mandatory. Concurrent completion can reorder the two
binary members inside a tarball even when both binaries are byte-deterministic.

A GoReleaser reproducibility snapshot must run against a committed, tagged
revision. An untagged snapshot reports commit `none` and falls back to
per-run mtimes, which the verifier rejects — it requires every archive member
mtime to equal `SOURCE_DATE_EPOCH`.

The main build's pre-hook builds each target's hosts helper and supplies its
SHA-256 through a target-specific Go overlay in a guarded staging directory;
the post-hook removes it. That keeps all four CLIs digest-paired to their exact
archive companion without modifying tracked source.

### Signing

The release flow uses GitHub Actions OIDC keyless Cosign:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity \
    'https://github.com/pgsty/farrow/.github/workflows/release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

cosign verify-blob-attestation checksums.txt \
  --bundle checksums.provenance.sigstore.json \
  --certificate-identity \
    'https://github.com/pgsty/farrow/.github/workflows/release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --type slsaprovenance1
```

Pin the exact identity and tag. Never verify with an open regular expression.

You can exercise the mechanism locally without any production identity:

```bash
./packaging/test-cosign-roundtrip.sh "$PWD/dist/linux-dev/checksums.txt"
```

It creates an encrypted ephemeral key in a guarded temporary directory,
verifies both the signature and SLSA provenance bundles, proves a tampered
checksum file fails both, and destroys the key directory on exit. Passing it
does not satisfy the production signing gate, and its artifacts are never
release artifacts.

### Tag release

`assemble-release.sh` combines the verified GoReleaser and Linux package
directories with the formula, release metadata and provenance predicate into
one final checksum manifest. The tag workflow signs and attests that manifest.

The workflow verifies the tag format, that the working tree is clean, and that
`git rev-list` matches `GITHUB_SHA`; uses pinned action digests and tool
versions; refuses to overwrite an existing release; and creates a **draft**
only.

Production private keys are not in this repository, and image manifest keys are
a separate trust domain from release signing keys.

## Conventions

- Strict parsing everywhere. Unknown fields are errors, not warnings.
- No shell strings. External programs get an argv slice and a deadline.
- Every destructive path is ownership- and path-bounded. If ownership cannot be
  proven, preserve and report.
- New dependencies need a documented purpose, a pinned version, a license
  review, and a standard-library alternative assessment.
