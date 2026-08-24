# Testing and evidence

Piglet uses these exact result classes: `unit`, `golden`, `integration`,
`native real E2E`, `not run`, `failed`, and `blocked`. Generated QEMU argv is
not a boot; a fake QMP server is not HVF/KVM; cross-builds are compile-only.

## Current source checks

Run on each native development runner:

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
govulncheck ./...
```

Compile, but never execute, non-native targets:

```bash
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=arm64 go build ./...
```

The 2026-08-24 Darwin arm64 run passed all five source checks, all four cross
builds, wrapper tests, and the 13-profile/85-node owned contract. `govulncheck` reported 0
reachable vulnerabilities (and one required-module advisory with no reachable
symbol). These checks must be rerun after any source change; the dated release
evidence records the final run rather than relying on this sentence.

## Tier-1 native ledger

| Test | macOS arm64 / HVF | Linux amd64 / KVM |
|---|---|---|
| M0 quick lifecycle | 10/10 pass | 10/10 pass |
| Public no-YAML quick | pass | pass; four business endpoints and ten cycles pass |
| Quick drift/reconcile | native pass | no dedicated negative matrix |
| Private network executor | socket_vmnet host install/reconnect/uninstall pass | clean Ubuntu bridge/helper install/uninstall/no residue pass |
| socket_vmnet FD path | composed product pass | not applicable |
| socket_vmnet shared | **pass** on clean `172.31.251.0/24`; old `10.10.10.0/24` run was confounded by vmnet sharing conflict | not applicable |
| Network preflight/custom `/24` | default reclaim + physical-overlap negative + public custom shared install/two-node/reconnect/rollback pass | default/custom native preflight + exact custom install dry-plan pass; custom host apply not run |
| Exact four-node `full` | pass | pass |
| Full crash/partial repair | pass | pass |
| Full 30-cycle soak | 30/30 pass | 30/30 pass |
| Exact MinIO | not run while unrelated VM owns `.10` | pass; 16/16 four-disk mounts and persistence |
| Formal seven-guest target | 7/7 pass | 7/7 pass with typed UEFI |
| Host reboot | not run | not run |

Every native evidence file records host/OS/arch, accelerator, QEMU/helper/
firmware, image digest, command, timestamps, result, retained evidence paths,
and explicit failed/not-run items where applicable.

Primary records:

- [`m1-quick-product-darwin-arm64-20260823.md`](evidence/m1-quick-product-darwin-arm64-20260823.md)
- [`m1-quick-product-linux-amd64-20260824.md`](evidence/m1-quick-product-linux-amd64-20260824.md)
- [`m2-private-full-product-darwin-arm64-20260824.md`](evidence/m2-private-full-product-darwin-arm64-20260824.md)
- [`m2-private-full-product-linux-amd64-20260824.md`](evidence/m2-private-full-product-linux-amd64-20260824.md)
- [`m2-private-minio-product-linux-amd64-20260824.md`](evidence/m2-private-minio-product-linux-amd64-20260824.md)
- [`m2-private-minio-product-darwin-arm64-dba-go127-20260824.md`](evidence/m2-private-minio-product-darwin-arm64-dba-go127-20260824.md)
- [`m2-private-minio-product-linux-amd64-dba-go127-isolated-20260824.md`](evidence/m2-private-minio-product-linux-amd64-dba-go127-isolated-20260824.md)
- [`m2-private-full-product-linux-amd64-dba-go127-isolated-20260824.md`](evidence/m2-private-full-product-linux-amd64-dba-go127-isolated-20260824.md)
- [`m2-private-full-product-darwin-arm64-dba-go127-20260824.md`](evidence/m2-private-full-product-darwin-arm64-dba-go127-20260824.md)
- [`m1-quick-product-linux-amd64-go127-isolated-20260824.md`](evidence/m1-quick-product-linux-amd64-go127-isolated-20260824.md)
- [`m1-quick-product-darwin-arm64-go127-20260824.md`](evidence/m1-quick-product-darwin-arm64-go127-20260824.md)
- [`m1-persistent-disk-key-purge-darwin-arm64-20260824.md`](evidence/m1-persistent-disk-key-purge-darwin-arm64-20260824.md)
- [`m1-no-wait-signal-fallback-darwin-arm64-20260824.md`](evidence/m1-no-wait-signal-fallback-darwin-arm64-20260824.md)
- [`m2-private-node-selectors-darwin-arm64-20260824.md`](evidence/m2-private-node-selectors-darwin-arm64-20260824.md)
- [`m2-private-drift-explicit-recreate-darwin-arm64-20260824.md`](evidence/m2-private-drift-explicit-recreate-darwin-arm64-20260824.md)
- [`m2-network-preflight-custom-subnet-20260824.md`](evidence/m2-network-preflight-custom-subnet-20260824.md)
- [`m2-network-preflight-hardening-20260824.md`](evidence/m2-network-preflight-hardening-20260824.md)
- [`m4-guest-matrix-darwin-arm64-20260824.md`](evidence/m4-guest-matrix-darwin-arm64-20260824.md)
- [`m4-guest-matrix-linux-amd64-20260824.md`](evidence/m4-guest-matrix-linux-amd64-20260824.md)

## Integration/security coverage

- Strict YAML/JSON unknown-field and trailing-data rejection.
- Atomic state, transaction journal, lock ordering, lease contention/reclaim.
- Schema-0→1 full preflight, dry-run, backup/apply, idempotence, newer-schema,
  running-process, and malformed-child refusal.
- qcow2 feature/backing/data-file/encryption rejection and offline-only checks.
- QMP interleave, process identity, crash repair, and fail-closed
  QMP-unavailable verified-process SIGTERM/SIGKILL/PID-reuse fallback.
- `/etc/hosts` and SSH global Include compare-and-swap, ownership, mode,
  symlink/hardlink, xattr/flags, marker conflict, and exact restoration.
- Linux networkd/NM/helper prestate restoration and Darwin pinned artifact/
  attestation/quarantine/notarization facts.
- Debug bundle canary redaction with key/seed/disk exclusion.
- Image-manifest multi-key signature, tamper, rollback, offline sync, cache and
  prune boundaries.
- Official-image pipeline immutable-copy, real/fake qemu inspection,
  backing/data/encryption/feature rejection, deterministic candidate bundle,
  and secret-free offline-tool boundary. Native libguestfs mutation is opt-in
  and must print `SKIP` rather than pass when its explicit source is absent.

Generated test secrets are canaries. Production keys, tokens, passwords, image
signing credentials, and user SSH material are never fixtures or log output.

## Profiles and Pigsty integration

```bash
make profile-contract
PIGSTY_SOURCE=/absolute/path/to/pigsty make pigsty-source-test
./tests/pigsty-wrapper-test.sh
```

The release contract is self-contained: strict catalog schema 3, exact profile
YAML/resolved hashes, 13 names, 85 VMs, final `dba` UID/GID88 identity, fixed
network/control/alias policy, ordinary 128 GiB data disks, MinIO four-by-32 GiB
disks, and typed inventory direct/subset/unused bindings. The opt-in Pigsty
source test validates all 13 default/custom templates, exact semantic token and
host counts, no-proxy/tuning behavior, sequential Makefile expansion, and
source-release exclusion.

Native U24 Darwin arm64 `meta` additionally passed bootstrap, inventory,
Ansible ping/become, and real deploy (`failed=0`, `unreachable=0`); see
[`m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md`](evidence/m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md).

## Release/package verification

The pinned tool versions are in `packaging/toolchain.env`.

```bash
./packaging/check-toolchain.sh all
goreleaser check
./packaging/verify-goreleaser.sh VERSION "$PWD/dist"
./packaging/verify-linux-packages.sh \
  VERSION COMMIT_OR_UNCOMMITTED SOURCE_DATE_EPOCH "$PWD/dist/linux-packages"
./packaging/test-cosign-roundtrip.sh "$PWD/dist/checksums.txt"
actionlint .github/workflows/release.yml
```

The GoReleaser verifier checks exact wrapped payloads, root/root archive
metadata, modes/architectures, target-specific helper digest injection, SPDX
archive checksum linkage, native version execution, and exact checksum
inventory. A committed synthetic snapshot with a configured remote produced
two byte-identical sets of four archives, four SPDX documents, and checksums.

The RPM/DEB verifier preflights archive paths and entry types before extraction,
requires exact payload/modes/architectures, proves CLI/helper digest pairing,
checks DEB metadata/recommendations, and requires payload SBOMs containing both
binaries and the reachable Go module graph. Separate Ubuntu 24.04 and Rocky
9.8 `--network none` containers perform install, package verification,
execution, remove/purge, and residue checks. These are package-native
integration tests, not VM/QEMU E2E.

Ephemeral Cosign tests create an encrypted key only in a guarded temporary
directory, verify signature and SLSA provenance bundles, prove tampered
checksums fail both verifiers, and remove the key. Passing this does not satisfy
the unrun production OIDC/custody gate.

## Evidence hygiene

Do not edit an old evidence result to turn a failure into a pass. Retain the
negative artifact, add the corrective run, and explain the causal change. Do
not label another platform supported when only a cross-build exists. The
authoritative readiness classification is
[`RELEASE_READINESS.md`](RELEASE_READINESS.md).

Dated final tooling details are in
[`m4-release-packaging-20260824.md`](evidence/m4-release-packaging-20260824.md)
and
[`m4-source-security-review-20260824.md`](evidence/m4-source-security-review-20260824.md).
