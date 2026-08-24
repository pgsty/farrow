# Pigsty bootstrap integration release rebuild — Go 1.27 — 2026-08-24

Result class: **current affected-source gates, two-run reproducible development
packaging, strict verification, and native packaged-wrapper execution**. This
is not a production tag, signature, attestation, or GA release.

## Source scope

This snapshot includes:

- catalog schema 3 inventory bindings and typed `rpm`/`deb` subset overlays;
- semantic custom `/24` inventory/VIP/service/DNS/NTP/no-proxy rebase;
- resource-aware inventory tuning and mode-0600 ownership/hash sidecars;
- official `dba` UID/GID88 cloud-init identity and readiness contract;
- reviewed Pigsty host aliases in standalone/installed SSH config;
- plan-after-destroy create semantics;
- PATH `pigsty-vm`, config-aware recreate, and package/archive layout;
- package SBOM coverage for the installed `pigsty-vm` script;
- neighboring Pigsty Makefile/source-release integration tests.

Affected packages passed unit and race tests. `go vet ./...`, Staticcheck
2026.2, four Darwin/Linux amd64/arm64 builds, GoReleaser check, Actionlint,
ShellCheck, profile contract, wrapper tests, and the opt-in Pigsty source corpus
passed. A fresh govulncheck database fetch was unavailable after the temporary
cache was cleared; the prior zero-reachable result remains the last complete
scan and no module version changed. The full socket-binding test suite could
not be rerun outside the sandbox after the approval-tool quota was exhausted;
this limitation is not counted as a pass.

## Reproducible snapshot

The real user worktree remains unborn/uncommitted. A source copy without
generated binaries or `dist` was committed/tagged only in a temporary mode-0700
Git repository:

```text
version:           0.1.2-next
synthetic commit:  a11db1dca5c5932d580713f7a17c67b10d56d623
SOURCE_DATE_EPOCH: 1787565600
date:              2026-08-24T10:00:00Z
Go:                1.27.0
```

The first attempted copy incorrectly excluded `cmd/piglet` and failed before
producing a release. The copy rule was corrected and the temporary commit/tag
was recreated; that failed attempt is not part of reproducibility evidence.

Two serial GoReleaser archive/SPDX runs, two DEB/RPM/payload-SPDX runs, and two
final assemblies were byte-identical. Every set passed its strict inventory,
mtime, architecture, mode, helper-pair, metadata, SBOM, and checksum verifier.
Ephemeral Cosign signature/provenance positive verification and tamper
rejection passed; its key and bundles were removed.

Retained outputs and checksum-manifest SHA-256:

```text
dist/goreleaser-pigsty-bootstrap-go127-20260824/       65cabed1145f3dc4eeda070b4b82ce7e89bce692784c8b6c01a28c80157221b9
dist/linux-pigsty-bootstrap-go127-20260824/            cdb5c2693666b8186fdc52e2888585bc124fce97a2199b92513667dfe4f408eb
dist/release-assembly-pigsty-bootstrap-go127-20260824/ 95bcb5e5273d04202f44b78ec80af20072a1502df7e7248a79e503c69b07862d
```

The final assembly has 19 checksummed assets plus the manifest and truthfully
records `signed:false`, `attested:false`.

The packaged Darwin arm64 CLI executed the exact version/commit/date. Its
packaged PATH `pigsty-vm` generated a custom `rpm` inventory with two hosts,
control/admin/etcd on `.9`, contiguous `infra_seq`, tiny tuning, and mode-0600
inventory/marker. Current DEB/RPM install/remove in Linux containers was not
initially available during the build step; it was subsequently completed on
the authorized Linux amd64 host. Exact disposable `--network none` Ubuntu
24.04.4 and Rocky 9.8 containers installed package version `0.1.2~next-1`,
executed the expected CLI identity, found `piglet` and PATH `pigsty-vm`, proved
their `init meta` output byte-identical, validated schema/profile/`dba`/eight
aliases, passed `dpkg -V`/`rpm -V`, and removed with no package/product residue.
The containers and remote staging directory were deleted.
