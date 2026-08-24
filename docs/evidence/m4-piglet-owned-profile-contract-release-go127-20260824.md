# Piglet-owned profile contract and rebuilt release assets — 2026-08-24

Result class: **current source gate, reproducible development packaging, and
isolated package install**. This is not a production tag, signature,
attestation, or GA release.

## Product boundary

Piglet now owns the complete profile/runtime contract:

- catalog schema 2 has no predecessor path or external source lock;
- all 13 embedded profiles use `dba` and export a `Piglet-owned profile`
  header;
- the old Ruby parity program and external-repository release fetch are gone;
- the release contract pins exact YAML digests, resolved semantic hashes,
  catalog references, 13 profiles, 85 nodes, network/control policy, and data
  disk invariants;
- `packaging/pigsty/vm` has one Piglet execution path.

Dated migration evidence remains provenance only. It is not a runtime, schema,
test, build, or release dependency.

## Go 1.27 source gate

The current tree passed:

```text
go test ./...                         PASS
go test -race ./...                   PASS
go vet ./...                          PASS
staticcheck 2026.2 ./...              PASS
govulncheck 1.7.0 ./...               PASS: 0 reachable vulnerabilities
Darwin/Linux x amd64/arm64 builds      PASS
make profile-contract                 PASS
Pigsty wrapper tests                  PASS
license-byte verification             PASS
goreleaser check                      PASS
Actionlint and ShellCheck              PASS
```

The first sandboxed test attempt was rejected by host socket-listen policy.
The same gate passed outside that restriction; no source change was used to
avoid the socket tests.

## Reproducible development snapshot

The user worktree remains an unborn repository. A source copy without `.git`,
`dist`, or generated binaries was committed and tagged only in a mode-0700
temporary directory:

```text
version:           0.1.1-next
synthetic commit:  7f3cf5f2c0d5db79bafc12cf518aaeed04bebe4d
SOURCE_DATE_EPOCH: 1787558400
date:              2026-08-24T08:00:00Z
Go:                1.27.0
```

Two serial GoReleaser runs produced byte-identical four-archive/four-SPDX sets
and both passed the strict archive, mtime, architecture, inventory, and paired
helper verifier. Two DEB/RPM builds produced byte-identical four-package/four-
SPDX sets and both passed the strict metadata, payload, mode, architecture,
SBOM, and helper-pair verifier. Two final assemblies were byte-identical.

Retained current outputs and checksum-manifest SHA-256:

```text
dist/goreleaser-piglet-owned-go127-20260824/       a8cc246b9ea592cd58d7d901774298322aad51b522f037edbfb91d8441f4fb2a
dist/linux-piglet-owned-go127-20260824/            0c8c1f426a237ec41d9c6bc0a3d6af3d7eedc76492e27692e642b004dee7e4e2
dist/release-assembly-piglet-owned-go127-20260824/ f8b4500fceeedaf7ab190caf1f614ccfbcb4bad557f3c73b839d2fb17415f464
```

The final assembly contains 19 checksummed assets plus `checksums.txt` and
truthfully records `signed:false` and `attested:false`. The local ephemeral
Cosign signature/provenance roundtrip passed positive verification and tamper
rejection; its temporary key and bundles were removed.

Archive and package binaries were independently scanned for the removed schema
fields and predecessor source paths. Darwin archive binaries also exported the
expected Piglet-owned profile header. The final amd64 DEB and RPM were then
installed in existing `--network none` Ubuntu 24.04 and Rocky 9.8 containers on
the authorized Linux amd64 test host. Both executed the exact version/commit,
exported `full` with `dba`, passed package verification, and removed with no
product path residue. The remote staging directory was removed.

The three superseded directories whose binaries still embedded the old catalog
schema were deleted after the current assets passed all checks. They are
rebuildable ignored outputs, not source or published releases.

## Remaining release boundary

Production image hosting/key custody, a durable macOS runner, a clean real
commit and `v1.0.0` tag, production OIDC/KMS publisher review, and published
Homebrew/RPM/DEB repository consumption remain open. These assets are a
verified development delivery, not v1.0 GA.
