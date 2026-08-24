# Owner scope + Go 1.27 delivery update — 2026-08-24

Result class: accepted product-scope change, full source regression, local image
corpus integration, reproducible packaging, and isolated Linux package install.
It does not claim a production tag, publisher identity, or signed GA release.

## Owner decisions implemented

- Quick is finalized as `dba` with a 64 GiB root and default sparse 64 GiB
  `/data`; migrated Pigsty profiles retain their explicit `vagrant`/disk parity.
- EL8 is removed from the v1 target, embedded manifest, and `rocky8` alias.
  Embedded manifest version is now `2026082402`, with seven aliases × two native
  architectures = 14 entries.
- At this checkpoint Darwin was recorded as host-only. A later clean
  alternate-subnet E2E superseded that conclusion: shared passes, while the
  default subnet is held by a conflicting vmnet sharing service.
- The build and language baseline is Go 1.27.0. Staticcheck 2026.2/v0.8.0 and
  govulncheck v1.7.0 are pinned as the matching quality tools.

ADR-0008 records the scope. Historical 8×2 guest evidence is unchanged; after
excluding EL8, every in-scope entry passed on its matching Tier-1 native host.

## Source and image verification

With Go 1.27.0 and isolated caches:

```text
go test ./...                         PASS
go test -race ./...                   PASS
go vet ./...                          PASS
staticcheck 2026.2 ./...              PASS
govulncheck 1.7.0 ./...               PASS: 0 reachable vulnerabilities
four native-target cross builds       PASS
ShellCheck / Actionlint                PASS
license-byte verification              PASS
13 profiles / 85 nodes parity         PASS
```

The opt-in local corpus test hashed and inspected all 14 in-scope qcow2 files
and passed in 14.7 seconds. `image list --json` reported manifest version
`2026082402`, 14 entries, and no EL8. `image info --json el8` returned
`unknown image alias "el8"`. The two old EL8 qcow files in the user cache were
not deleted.

The final local Go 1.27 helper SHA is
`4d218aef0f83baf20a9bd78d8e2bc76b1bae084cb0371ab22be6d7f6be6f8942`.
The fixed root:wheel mode-0755 helper has the same digest, and the CLI contains
that exact companion digest.
The checkpoint's root read-only network status reported the pinned
socket_vmnet service and private route `ok`, with the global lease inactive.
That status check and the public shared rejection were later corrected: a
listening socket alone does not prove vmnet startup, and current source accepts
shared while requiring the host address.

## Reproducible Go 1.27 artifacts

An isolated synthetic Git repository was used only to exercise GoReleaser:

```text
commit: 761e06e6585a4baa3df32108dba97ff4176d7df6
SOURCE_DATE_EPOCH: 1787534786
snapshot version: 0.1.1-next
```

Two serial GoReleaser runs were byte-identical and passed the exact archive,
SPDX, architecture, mode, and helper-pair verifier. Checksums manifest SHA-256:

```text
881a69448359ebcb66b4a7cd17e437af68786e74000e3d772ae8a8a996d0589e
```

Two RPM/DEB builds were byte-identical and passed payload/SPDX/metadata/pairing
verification. Package checksums manifest SHA-256:

```text
eb8a72d1bd9b1bf64ad856f2a1b14b51bde75bf4e9123a379748a05732988c25
```

Two development archive/formula builds were byte-identical. Their checksum
manifest SHA-256 is:

```text
cf8a88091d87ec1a47c92482e8a278aa93e35958adc8150a95fb07ad8d975c75
```

The final 19-entry assembly manifest SHA-256 is:

```text
1eb27fe5e576efd070d211733e78e34a946d892bb9298ff60ca1bd6772520f1e
```

Ephemeral Cosign signature and SLSA provenance bundles passed positive
verification and tamper rejection through the local proxy; the temporary key
directory was removed. `release.json` remains truthfully `signed:false` and
`attested:false` because no production workflow ran.

On the authorized Linux amd64 host, the exact Go 1.27 DEB and RPM were installed
inside existing `--network none` Ubuntu 24.04 and Rocky 9.8 containers. Both
executed the expected commit/date, passed package verification, shipped six
license files, removed cleanly, and left the host package paths absent.

## Current retained outputs

```text
dist/goreleaser-go127-scope-20260824/          9 files
dist/linux-go127-scope-20260824-a/             9 files
dist/dev-go127-scope-20260824-a/               7 files
dist/release-assembly-go127-scope-20260824/   20 files
```

These are ignored development/snapshot artifacts, not published GA assets.
They predate the later shared-mode/subnet-conflict correction and remain
historical mechanism evidence. Current rebuilt assets superseding them are
recorded in
[`m4-netpreflight-release-packaging-20260824.md`](m4-netpreflight-release-packaging-20260824.md).
