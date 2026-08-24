# Piglet v1.0 release readiness — 2026-08-24

Overall result: **not ready / blocked at GA gates**. This ledger uses
`PASS`, `PARTIAL`, `FAILED`, `BLOCKED`, and `NOT RUN`; a build or mock is never
substituted for native evidence.

## PRD §17.1 — Quick

| Acceptance item | macOS arm64 | Linux amd64 | Result |
|---|---|---|---|
| No-config `piglet up` | current Go 1.27 native HVF pass | current Go 1.27 native KVM isolated-host pass | PASS |
| QEMU native accelerator and non-root | pass | pass | PASS |
| SSH, exec, outbound network | pass | pass | PASS |
| CPU/memory/root match resolved state | pass | pass | PASS |
| Default `/data` | 64 GiB pass | 64 GiB pass | PASS: owner-finalized baseline |
| Login user | name `dba` passed; new UID/GID88 Quick refresh not run | name `dba` passed; new UID/GID88 Quick refresh not run | PARTIAL: private U24 meta proves final numeric identity |
| `--no-data-disk` | native pass | not separately run natively | PARTIAL |
| Four default loopback forwards | materialized; custom forward native pass | all four token endpoints native pass | PARTIAL: Darwin lacks a four-endpoint token run |
| Port-conflict rematerialization | native drift pass | no dedicated native collision injection | PARTIAL |
| Stop/start data persistence | pass | pass, including ten cycles | PASS |
| Single direct-QEMU execution chain; no secondary VM orchestrator | pass | pass | PASS |
| Guarded destroy preserves cache/key/persistent data | current native preserve/reattach/recreate/delete/key-purge pass | cache/key and scope pass; shared persistent store unit/race | PASS for Quick contract |

Primary evidence:

- [`m1-quick-product-darwin-arm64-go127-20260824.md`](evidence/m1-quick-product-darwin-arm64-go127-20260824.md)
- [`m1-quick-product-linux-amd64-go127-isolated-20260824.md`](evidence/m1-quick-product-linux-amd64-go127-isolated-20260824.md)
- [`m1-drift-reconcile-darwin-arm64-20260823.md`](evidence/m1-drift-reconcile-darwin-arm64-20260823.md)
- [`ADR-0001`](decisions/0001-authority-and-quick-defaults.md)
- [`ADR-0008`](decisions/0008-v1-owner-scope-20260824.md)

## PRD §17.2 — Exact private `full`

The pre-UID88 all-`dba` `profiles/full.yaml` VM semantics passed Darwin HVF and Linux
native-KVM isolation. The table combines those topology results with
the earlier bare-host crash/repair and 30-cycle evidence:

| Acceptance item | Darwin arm64 host mode | Linux amd64 bridge | Result |
|---|---|---|---|
| Fixed `.10`–`.13`, host direct access | pass | pass | PASS |
| VM peer traffic on private NIC | pass | pass | PASS |
| Guest internet via management NAT | pass | pass | PASS |
| Private route, no private default route/DNS | pass | pass | PASS |
| NAT SSH fallback 4/4 | pass | pass | PASS |
| Control-only lateral SSH key | pass | pass | PASS |
| 64 GiB root + 128 GiB ordinary data | 8 disks pass | 8 disks pass | PASS |
| Second project exits 6 | pass | pass | PASS |
| Privileged helper ownership and non-root QEMU | pass | pass | PASS |
| Partial failure / crash / repair | pass | pass | PASS |
| 30-cycle leak soak | 30/30 | 30/30 | PASS |

Evidence:

- [`m2-private-full-product-darwin-arm64-20260824.md`](evidence/m2-private-full-product-darwin-arm64-20260824.md)
- [`m2-private-full-product-linux-amd64-20260824.md`](evidence/m2-private-full-product-linux-amd64-20260824.md)
- [`m2-private-full-product-darwin-arm64-dba-go127-20260824.md`](evidence/m2-private-full-product-darwin-arm64-dba-go127-20260824.md)
- [`m2-private-full-product-linux-amd64-dba-go127-isolated-20260824.md`](evidence/m2-private-full-product-linux-amd64-dba-go127-isolated-20260824.md)
- [`m2-private-integrations-recovery-darwin-arm64-20260824.md`](evidence/m2-private-integrations-recovery-darwin-arm64-20260824.md)

Darwin socket_vmnet shared mode now has a native two-node **PASS** on a clean
alternate subnet. The earlier default-subnet failure was confounded; the host
later reported `VMNET_SHARING_SERVICE_BUSY (1009)`. Product install no longer
rejects shared categorically, and guest private NICs disable IPv6 RA. Typed
preflight and one explicit warned custom RFC1918 `/24` escape hatch are now
implemented. Darwin public custom install/two-node/reconnect/rollback passed;
Linux native custom dry-plan passed on the earlier source. Current code now
refuses to start inactive networkd unless every effective rule is proven
disjoint; `ai` correctly stopped on its Wi-Fi rule before mutation, so a new
clean-host apply remains not run. See
[`shared`](evidence/m0-b-darwin-shared-clean-subnet-20260824.md) and
[`preflight/custom`](evidence/m2-network-preflight-custom-subnet-20260824.md),
plus [`Linux activation safety`](evidence/m2-linux-networkd-activation-safety-20260824.md).
Current Darwin install additionally binds the observed BSD interface to its
persistent UUID/CIDR in public and protected root-owned markers; a foreign
VirtualBox-shaped exact `.1/24` fixture remains an exit-6 conflict. See
[`interface ownership`](evidence/m2-darwin-interface-ownership-preflight-20260824.md).

## PRD §17.3 — Release

| Gate | Current fact | Result |
|---|---|---|
| Tier-1 quick/full traceability | dated product evidence on both hosts | PASS |
| Formal guest matrix | Current UID/GID88 seed and exact manifest inputs passed native lifecycle/data/network/time/identity on Linux 7/7 and Darwin 7/7 | PASS: Tier-1 14/14 current entries |
| Unit/race/vet/staticcheck | final frozen current tree passed on Darwin arm64 | PASS |
| Four target builds | current tree compiles Darwin/Linux × amd64/arm64 | PASS, compile-only for non-native targets |
| Vulnerability scan | `govulncheck ./...`: 0 reachable vulnerabilities | PASS |
| Owned profile/inventory contract | schema 3, exact YAML/resolved hashes, 13 profiles / 85 VMs, typed direct/subset/unused bindings | PASS: all custom inventories parse with exact host counts; no external predecessor dependency |
| GoReleaser | current Pigsty-bootstrap snapshot, PATH wrapper, fixed mtimes, paired helper, exact archives/SPDX, two-run byte identity | PARTIAL: current mechanism passes; no real release tag/publish |
| RPM/DEB | current PATH wrapper, paired helper, complete payload SBOM, two-run identity, strict extraction, no-network Ubuntu/Rocky install/verify/remove | PARTIAL: current local mechanism passes; no published repositories |
| Homebrew | strict formula renderer and Ruby syntax pass | PARTIAL: no published bottle/tap install |
| Signature/provenance | ephemeral Cosign v3 signature and SLSA-bundle positive/negative tests pass | PARTIAL: production OIDC workflow is checked in but unrun |
| State N-1 | schema-0 backup/dry-run/apply/idempotence/newer-refusal and stopped-state guards pass | PARTIAL: no prior production release state exists |
| Install/network/image/migration/security/troubleshooting docs | present | PASS for documentation presence; update with every gate |
| Clean release commit/tag | local clean commits now exist; no remote, production tag, or publisher is configured | BLOCKED at publication/tag gate |

Release/source evidence:

- [`m4-owner-scope-go127-20260824.md`](evidence/m4-owner-scope-go127-20260824.md)
- [`m4-release-packaging-20260824.md`](evidence/m4-release-packaging-20260824.md)
- [`m4-source-security-review-20260824.md`](evidence/m4-source-security-review-20260824.md)
- [`m4-netpreflight-release-packaging-20260824.md`](evidence/m4-netpreflight-release-packaging-20260824.md)
- [`m4-final-source-gates-go127-20260824.md`](evidence/m4-final-source-gates-go127-20260824.md)
- [`m4-piglet-owned-profile-contract-release-go127-20260824.md`](evidence/m4-piglet-owned-profile-contract-release-go127-20260824.md)
- [`m4-uid88-guest-refresh-linux-amd64-el9-d13-go127-20260824.md`](evidence/m4-uid88-guest-refresh-linux-amd64-el9-d13-go127-20260824.md)
- [`m4-uid88-guest-refresh-linux-amd64-remaining-go127-20260824.md`](evidence/m4-uid88-guest-refresh-linux-amd64-remaining-go127-20260824.md)
- [`m4-uid88-guest-refresh-darwin-arm64-go127-20260824.md`](evidence/m4-uid88-guest-refresh-darwin-arm64-go127-20260824.md)
- [`m4-image-normalization-pipeline-go127-20260824.md`](evidence/m4-image-normalization-pipeline-go127-20260824.md)
- [`m1-openssh-space-path-regression-go127-20260824.md`](evidence/m1-openssh-space-path-regression-go127-20260824.md)
- [`m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md`](evidence/m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md)
- [`m4-pigsty-bootstrap-release-go127-20260824.md`](evidence/m4-pigsty-bootstrap-release-go127-20260824.md)

Formal matrix evidence:

- [`m4-guest-matrix-linux-amd64-20260824.md`](evidence/m4-guest-matrix-linux-amd64-20260824.md): historical run 8/8; current target 7/7.
- [`m4-guest-matrix-darwin-arm64-20260824.md`](evidence/m4-guest-matrix-darwin-arm64-20260824.md): historical run 7/8; current target 7/7. The excluded EL8 arm64 failure remains evidence, not a release gate.
- [`ADR-0008`](decisions/0008-v1-owner-scope-20260824.md): owner scope decision.

## M3/M4 items not closed

| Item | Result |
|---|---|
| Embedded init/scale/image policy and wrapper fake tests | PASS |
| `--no-wait` and verified QMP-unavailable signal fallback | PASS: native fault injection plus deterministic SIGTERM/SIGKILL/PID-reuse tests |
| Private node selectors | PASS for plan/up/start/stop/restart/recreate/status/repair/ssh-config at unit/integration level; standalone partial destroy remains unsupported and native partial recreate is NOT RUN |
| Persistent disk and key retirement | PASS: native preserve/reattach/recreate/explicit delete/purge plus unit/race negatives |
| Private desired-state drift | PASS for typed conflict and explicit full recreate; stopped-only in-place optimization NOT IMPLEMENTED |
| SSH config and `/etc/hosts` secure native roundtrip | PASS |
| Exact current `full` profile (`dba`) | topology/storage passed both Tier-1 hosts before UID/GID88/alias seed change; current identity refresh NOT RUN |
| Exact current `minio` four-disk profile (`dba`) | topology/storage passed both Tier-1 hosts before UID/GID88/alias seed change; current identity refresh NOT RUN |
| Pigsty bootstrap in Piglet guest | native U24 Darwin arm64 meta PASS: deploy `failed=0`, sustained service/backup health, canary stop/start, scoped destroy/key purge, inactive lease |
| Tier-2 Darwin amd64 native smoke | NOT RUN |
| Tier-2 Linux arm64 native smoke | NOT RUN |
| Host reboot persistence | NOT RUN on both Tier-1 hosts |
| RPM-family private-network host | NOT RUN |
| Linux custom-subnet privileged apply | NOT RUN on disposable native runner; `ai` read-only preflight/dry-plan PASS |
| Lima same-image oracle comparison | NOT RUN |
| Image build/self-host migration pipeline artifact | local validate/rejection/reproducibility pipeline PASS, including real qemu-img; native libguestfs normalization and production upload/signing NOT RUN |

## External and owner gates

1. Production image object storage/domain/bandwidth and active/standby manifest
   key custody are not assigned.
2. Production Cosign/OIDC/KMS release custody and two-person publication review
   are not assigned; the checked-in workflow has never run.
3. A durable self-hosted macOS arm64 HVF runner owner is not assigned.

Resolved owner decisions on 2026-08-24:

- Quick is 64 GiB + `dba`.
- Every embedded profile uses `dba`; official guest identity is UID/GID 88,
  and the PATH `pigsty-vm`/Pigsty Makefile path is Piglet-only.
- EL8 is outside the v1 target; the formal matrix is 14 entries.
- Darwin host remains the default; shared passed the same contract. A warned,
  coordinated custom RFC1918 `/24` is the collision escape hatch.

Until these failed/blocked/not-run entries are resolved, no v1.0 tag should be
created.
