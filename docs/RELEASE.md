# Release readiness and supply-chain policy

Piglet is not GA merely because binaries compile. A v1.0 release requires the
full Section 17 evidence ledger, a clean committed source revision, an exact
`v1.0.0` tag, owner-controlled image/signing infrastructure and no P0 blocker.

## Build and package gates

```bash
make check
govulncheck ./...
./packaging/check-toolchain.sh all
goreleaser check

SOURCE_DATE_EPOCH=<tag-commit-time> \
goreleaser release --clean --skip=publish --parallelism 1

SOURCE_DATE_EPOCH=<tag-commit-time> \
./packaging/verify-goreleaser.sh 1.0.0 "$PWD/dist"

SOURCE_DATE_EPOCH=<tag-commit-time> \
PIGLET_COMMIT=<full-commit> \
./packaging/build-linux-packages.sh 1.0.0 dist/linux-1.0.0

./packaging/verify-linux-packages.sh \
  1.0.0 <full-commit> <tag-commit-time> "$PWD/dist/linux-1.0.0"
```

`packaging/build-release.sh` is deliberately development-only and refuses a
stable version. GoReleaser is the GA archive authority. It defines four wrapped
native archives and SPDX SBOM generation through Syft. nFPM produces root-owned
RPM/DEB payloads for linux/amd64 and linux/arm64. The final assembler combines
archives, packages, both SBOM sets, Homebrew formula, release metadata, and the
provenance predicate into one 19-entry checksum manifest.

Set `SOURCE_DATE_EPOCH` to the tag commit time for GoReleaser as well. Its SBOM
wrapper retains Syft's catalog but replaces the random document namespace with
the archive SHA-256 and the scan time with that fixed epoch before GoReleaser
calculates checksums.

Snapshot reproducibility tests must also use a local tag pointing at the exact
synthetic commit. An untagged GoReleaser snapshot may report commit `none` and
fall back to per-run binary mtimes. `verify-goreleaser.sh` therefore requires
every archive file member mtime to equal `SOURCE_DATE_EPOCH`; an individually
valid archive with build-time mtimes is rejected before it can count as
reproducible evidence.

The GoReleaser main-build pre-hook independently builds the target helper and
supplies its SHA-256 through a target-specific Go overlay under an ignored,
guarded staging directory; its post-hook removes that target staging directory.
This keeps all four GoReleaser CLIs fail-closed to their exact archive companion
without modifying tracked source. Snapshot/release verification must recompute
each archived helper digest and find that exact digest in its paired CLI.
Release builds fix GoReleaser parallelism to 1 because concurrent completion
can otherwise reorder the two binary members inside a tarball even when both
binary bytes are deterministic.

The exact tool versions are pinned in `packaging/toolchain.env` and written to
`release.json`. Tool updates require fresh two-run reproducibility evidence.

## Signing and attestations

Production private keys never enter source, tests, build logs or ordinary CI
secrets. The preferred release flow is GitHub Actions OIDC keyless Cosign:

```bash
cosign sign-blob --yes --bundle checksums.txt.sigstore.json checksums.txt
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity \
    'https://github.com/pgsty/piglet/.github/workflows/release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

cosign verify-blob-attestation checksums.txt \
  --bundle checksums.provenance.sigstore.json \
  --certificate-identity \
    'https://github.com/pgsty/piglet/.github/workflows/release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --type slsaprovenance1
```

The bundle, SBOMs, checksums and provenance predicate are release assets.
Self-managed/KMS signing is acceptable only with named custody, offline
recovery, rotation and two-person release review. Local ephemeral keys may test
the mechanism, but those artifacts stay development-only.

Run `packaging/test-cosign-roundtrip.sh` against an absolute checksums path to
exercise the Cosign v3 signature and SLSA provenance bundle paths plus negative
tamper verification. The script uses no production identity and destroys its
guarded temporary key directory on exit; passing it does not satisfy the
production signing or attestation gate.

Image-manifest minisign keys are a separate active/standby custody boundary.
An application release signature does not authorize an image manifest and vice
versa.

`.github/workflows/release.yml` is fail-closed to an exact tag/commit, pinned
tool versions, strict package/archive verifiers, and the exact workflow OIDC
identity. It refuses to overwrite an existing release and creates only a draft.
It has not run in this unborn worktree; checked-in workflow logic is not
production custody evidence.

## Evidence gates

- Tier-1 quick and `full` public product E2E, including crash and 30-cycle
  soak, on macOS arm64 and Linux amd64.
- Seven formal guests on both native Tier-1 architectures; only entries with
  real traceable smoke may move from `testing` to `supported`.
- Tier-2 compile/golden and periodic native smoke.
- Piglet-owned profile topology contract, intentional `dba` identity
  normalization, and Pigsty bootstrap.
- State N-1 backup/apply/downgrade-protection tests.
- Dependency/license/security review, package install/uninstall tests and
  complete install/network/image/migration/troubleshooting docs.

## External owner gates

The repository cannot invent image hosting/bandwidth, production signing-key
custody, or a durable self-hosted macOS HVF runner. Until those are assigned
and evidenced, release metadata must remain unsigned/unattested development
status and no v1.0 tag should be created.
