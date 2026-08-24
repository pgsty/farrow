# Current Go 1.27 release/package rebuild — 2026-08-24

Result class: **current-source reproducible release mechanism pass**. This is a
development snapshot, not a production tag, signature identity, repository
publication, or GA declaration.

## Immutable test identity

The current uncommitted worktree was copied into a guarded temporary repository
and committed solely for release-mechanism verification:

```text
synthetic commit: 8658a5d7889310dfc60712dd3c170d109e807062
local test tag:   v0.1.0
snapshot version: 0.1.1-next
SOURCE_DATE_EPOCH: 1787545808
Go: 1.27.0
```

No tag, commit, remote, or release was created in the user worktree.

## Reproducibility defect found and fixed

Two initial untagged GoReleaser snapshots each passed the old verifier, but all
eight checksummed assets differed. Binary bytes were identical; raw tar
comparison isolated the difference to the two binary member mtimes. Without a
tag, GoReleaser snapshot metadata used commit `none`, so the configured commit
timestamp degraded to each run's build time.

The snapshot was given a local test tag and canonical origin metadata. All
archive members then used the fixed commit epoch. The verifier was strengthened
to require every extracted file mtime to equal `SOURCE_DATE_EPOCH`; it rejects
the original negative-control archives at the first non-fixed member.

This matches [GoReleaser's reproducible-build guidance](https://goreleaser.com/customization/builds/builders/go/#reproducible-builds)
to use stable commit timestamps for binary/archive metadata. Production
workflow tags already provide that identity.

## GoReleaser pair

Two serial tagged runs were byte-identical and independently passed exact
inventory, wrapped layout, regular-file-only, mode, architecture, Go version,
CLI/helper digest pairing, SPDX content, and checksum verification.

```text
checksums.txt SHA-256:
6ee831ce0b18f2a26b5b8e70a65899daa29531de76837655566854ee8bf0a1f5
```

Target helper digests:

```text
darwin/amd64 53649994e0d73765caf1de0ed9c32e5db74d6cb6e58b5d60fc24a8a96278b50b
darwin/arm64 5ca5c7d27c63cc59bf421c52ff42afd373694104ea605c068d3ac8c2e8f56265
linux/amd64  bc9e494dfcd44b89f62be040c85dd28a72758775ce4385cbea336c2f98bd9a7d
linux/arm64  2269313a3b38b31037a7d7fbc5bef737690f4558b4c4caf27fa4538b1b89f7f5
```

## RPM/DEB pair

Two builds were byte-identical and passed:

- exact payload/type/path/mode/architecture checks;
- DEB control and dependency/recommends policy;
- embedded Go 1.27 version and source identity;
- CLI/helper digest pairing and DEB/RPM payload parity;
- payload-directory SPDX with Piglet Go modules and both installed binaries.

```text
checksums.txt SHA-256:
9251eb2b1f18d93eb09fa5d710da9b7af1a5bffb0581a851fdc03f369c5a07ba
```

The exact amd64 DEB and RPM were installed, executed, and removed in
`--network none` Ubuntu 24.04 and Rocky 9.8 containers on `ai`. Both printed
version `0.1.1-next`, full commit `8658a5d...`, and build time derived from the
fixed epoch. Package paths were absent after removal and the `ai` host itself
was never installed or modified.

## Final assembly and signing mechanism

Two assemblies from the independent pairs were content-identical. Each
contains 19 checksummed assets plus `checksums.txt`: four archives, four archive
SBOMs, four packages, four package SBOMs, Homebrew formula, `release.json`, and
the SLSA provenance predicate.

```text
final checksums.txt SHA-256:
27f418b81cdfeb0a244e0a72c677990c1038eca9d8b62d5e43abfd7cb0f4694d
```

All 19 entries verified, formula Ruby syntax passed, release metadata names the
exact commit/epoch/Go 1.27 toolchain, and provenance resolves the same commit.
The development metadata truthfully remains `signed:false` and
`attested:false`.

Ephemeral Cosign v3 signature and SLSA attestation bundles verified; tampered
checksum input was rejected by both paths and the temporary key directory was
removed. A Ruby injection URL containing quotes/semicolon was rejected before
formula output was written.

## Retained current outputs

```text
dist/goreleaser-netpreflight-go127-20260824/          9 files
dist/linux-netpreflight-go127-20260824/               9 files
dist/release-assembly-netpreflight-go127-20260824/   20 files
```

These ignored snapshot assets contain the current preflight/custom-subnet tree.
They must not be published as GA; production OIDC identity/custody, a real
clean tag, external release gates, and repository publication remain required.
