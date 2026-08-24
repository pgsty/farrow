# Piglet v1.0 delivery ledger

This file maps `docs/003-prd.md` and the active goal to implementation and
evidence. Status vocabulary is exactly `done`, `in progress`, `not run`,
`failed`, and `blocked`. Milestones are checkpoints, not completion claims.

The detailed §17 decision table is
[`docs/RELEASE_READINESS.md`](docs/RELEASE_READINESS.md).

## Milestones

| Milestone | Status | Current evidence / remaining gate |
|---|---|---|
| M0 technical spikes | done for selected product paths | Quick HVF/KVM, Darwin host/shared default+custom, Linux bridge, and guest renderers passed. Lima oracle remains not run. |
| M1 quick MVP | in progress | Current Go 1.27 no-YAML Quick passed Darwin HVF and Linux native-KVM isolation with 64 GiB + `dba`. Several negative/native variants remain. |
| M2 private multi-node | in progress | Current all-`dba` full and MinIO passed both Tier-1 VM semantics; historical bare-host evidence covers recovery, lease and 30 cycles. Host reboot and RPM-family private host remain. |
| M3 Pigsty migration | in progress | Schema-3 profile/inventory bindings, single-runtime Pigsty Makefile, custom subnet/VIP/no-proxy rebase, dba UID/GID88, 13-inventory parsing, native meta deploy, health, stop/start, and cleanup pass. Full-lab bootstrap remains. |
| M4 GA | blocked | Current seven-guest formal target is 14/14. Release mechanisms pass locally, but production custody/runner and unrun current-code gates prevent GA. |

## Requirements

| ID | Requirement | Implementation and verification | Status | Evidence |
|---|---|---|---|---|
| G-001 | Zero-config quick | Public lifecycle, QMP/SSH/exec, user NAT, data persistence, forwards, drift, destroy | in progress | [`Darwin current`](docs/evidence/m1-quick-product-darwin-arm64-go127-20260824.md), [`Linux current`](docs/evidence/m1-quick-product-linux-amd64-go127-isolated-20260824.md), [`ADR-0001`](docs/decisions/0001-authority-and-quick-defaults.md), [`ADR-0008`](docs/decisions/0008-v1-owner-scope-20260824.md) |
| G-002 | Declarative private lab | Strict spec, one global lease, dual NIC, parallel four-node lifecycle | done for current exact `full` VM semantics; broader M2 in progress | [`Darwin current full`](docs/evidence/m2-private-full-product-darwin-arm64-dba-go127-20260824.md), [`Linux current full`](docs/evidence/m2-private-full-product-linux-amd64-dba-go127-isolated-20260824.md) |
| G-003 | Own the Pigsty VM surface | Embedded all-`dba` profiles, typed inventory bindings, PATH `pigsty-vm`, constrained scale/image/subnet, Piglet-only Pigsty Makefile | in progress | [`native meta bootstrap/deploy`](docs/evidence/m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md) passes; full-lab bootstrap/cleanup pending |
| G-004 | Reproducible self-hosted images | Immutable dated manifest, digest cache/import, explicit signed sync, offline normalization candidate pipeline, 14 in-scope sources | in progress | [`normalization pipeline`](docs/evidence/m4-image-normalization-pipeline-go127-20260824.md), [`signed manifest`](docs/evidence/m1-signed-manifest-20260823.md); native normalization/hosting/custody blocked or not run |
| G-005 | Safe/recoverable/diagnosable | Atomic state/journal, process identity, repair, strict deletion, doctor/debug/redaction | done for exercised paths | recovery and full evidence; host reboot remains not run |
| FR-001 | Probe and doctor | Real accelerator smoke, exact-key capability cache, all-node journals, typed route/interface/backend ownership, QEMU/firmware/storage | in progress | [`current HVF/cache doctor`](docs/evidence/m1-doctor-hvf-runtime-go127-20260824.md), [`marker-bound Darwin interface`](docs/evidence/m2-darwin-interface-ownership-preflight-20260824.md), [`custom subnet`](docs/evidence/m2-network-preflight-custom-subnet-20260824.md); richer desired-image/port compatibility remains |
| FR-010 | Manifest/download/import | HTTPS bounds, SHA/artifact size, safe qcow2 features, two-key minisign sync/rollback | done with development roots | production source/keys blocked |
| FR-020 | Project/state/locks | Safe configured-root precedence, marker-locked migration boundary, strict versioned JSON, atomic writes, lock ordering | done | [`config/runtime closure`](docs/evidence/m1-config-runtime-safety-go127-20260824.md), unit/race plus both product paths |
| FR-030 | Transactions/recovery | Per-node journals, scoped rollback, crash repair, mixed-state lifecycle | done for tested paths | both full/recovery evidence |
| FR-040 | Root/data disks | Explicit qcow2 backing, offline resize/check, deterministic serial/UUID/fstab | done for non-persistent disks | quick/full matrices |
| FR-045 | Persistent data lifecycle | Destroy/recreate preserve `persistent: true`; explicit `--delete-persistent` double confirmation | done for Quick native and shared quick/private store | [`persistent/key purge`](docs/evidence/m1-persistent-disk-key-purge-darwin-arm64-20260824.md); private unit/race coverage |
| FR-050 | NoCloud CIDATA | Pure-Go ISO, per-family network render, dba UID/GID88 identity gate, ready generation, control-only key | done; current native numeric identity evidence 14/14 | [`Darwin remaining six`](docs/evidence/m4-uid88-guest-refresh-darwin-arm64-go127-20260824.md), [`Linux remaining five`](docs/evidence/m4-uid88-guest-refresh-linux-amd64-remaining-go127-20260824.md), [`EL9 + D13 Linux`](docs/evidence/m4-uid88-guest-refresh-linux-amd64-el9-d13-go127-20260824.md), [`U24 Darwin meta`](docs/evidence/m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md) |
| FR-060 | Native platform/firmware | Native-only tuples, no TCG, typed BIOS/UEFI, QEMU floor probes | done | current 7/7 per Tier-1 arch; historical runs also covered EL8 |
| FR-070 | Process/QMP | Bounded argv, detach, QMP multiplex/identity; verified-process signal fallback | done | [`native no-wait/fallback`](docs/evidence/m1-no-wait-signal-fallback-darwin-arm64-20260824.md), deterministic SIGTERM/SIGKILL/PID-reuse tests |
| FR-075 | Lifecycle selection/wait | Resolved timeout, typed plan/up/start/stop/restart/recreate/status/repair/ssh-config selectors, and `--no-wait` | in progress | [`config/runtime closure`](docs/evidence/m1-config-runtime-safety-go127-20260824.md), [`--no-wait`](docs/evidence/m1-no-wait-signal-fallback-darwin-arm64-20260824.md), [`selected lifecycle`](docs/evidence/m2-private-node-selectors-darwin-arm64-20260824.md); standalone partial destroy remains unsupported |
| FR-080 | SSH | Resolved login user, quoted per-project keys/known_hosts on space paths, exit passthrough, multi-node fragments, safe Include | done | [`native macOS default-path regression`](docs/evidence/m1-openssh-space-path-regression-go127-20260824.md), [`config/runtime closure`](docs/evidence/m1-config-runtime-safety-go127-20260824.md), both Tier-1 full integrations |
| FR-085 | Key retirement | `project purge-keys` only after no running or retained nodes/disks | done | [`persistent/key purge`](docs/evidence/m1-persistent-disk-key-purge-darwin-arm64-20260824.md), unit/race integrity negatives |
| FR-090 | User network | Deterministic SSH/business ports, conflict materialization | in progress | Linux four-endpoint pass; Darwin/Linux dedicated negative coverage incomplete |
| FR-100 | Darwin private | socket_vmnet 1.2.2, marker-bound interface ownership, host/shared, custom `/24`, reconnect, FD fallback | done on arm64; Intel native remains | [`interface ownership`](docs/evidence/m2-darwin-interface-ownership-preflight-20260824.md), [`host`](docs/evidence/m0-b-darwin-private-native-20260823.md), [`FD`](docs/evidence/m0-b-darwin-fd-product-20260824.md), [`custom`](docs/evidence/m2-network-preflight-custom-subnet-20260824.md) |
| FR-110 | Linux private | networkd/NM/bridge-helper plan, inactive-networkd non-system-link proof, custom `/24`, install/status/uninstall/restore | in progress: earlier Ubuntu lifecycle passed; current unsafe-host refusal passed | [`activation safety`](docs/evidence/m2-linux-networkd-activation-safety-20260824.md), [`executor`](docs/evidence/m2-linux-network-product-executor-20260824.md), [`custom dry-plan`](docs/evidence/m2-network-preflight-custom-subnet-20260824.md); current clean-host apply/RPM-family host not run |
| FR-120 | Lease/multi-node | Atomic exit-6 lease, bounded concurrency, `up --rollback` for safe current failed-prepare journals, partial recovery, 30-cycle soak | in progress | failed-prepare rollback unit/integration passes and both full soaks pass; subset destroy/recreate remains |
| FR-130 | Drift/plan | Canonical hash, stopped reconcile, running conflict/restart/recreate | in progress | Quick fine-grained drift passed; [`private explicit recreate`](docs/evidence/m2-private-drift-explicit-recreate-darwin-arm64-20260824.md) passed; private stopped-only in-place optimization remains |
| FR-140 | Logs/diagnostics | Event/QEMU/serial logs and canary-redacted debug bundle | done for exercised paths | [`debug`](docs/evidence/m1-list-debug-darwin-arm64-20260823.md), full evidence |
| State N-1 | Backup-first schema migration | Explicit dry-run/apply, full strict preflight, stopped/no-lease guard, idempotence, newer refusal | in progress | unit/integration passed; prior released schema fixture unavailable |
| Profile/inventory contract | Piglet-owned topology plus final `dba` identity | catalog schema 3, exact YAML/resolved hashes, typed direct/subset/unused bindings, 13 profiles, 85 VMs | done for contract and Ansible parsing | `make profile-contract`; `PIGSTY_SOURCE=... make pigsty-source-test`; native meta deploy passes |
| MinIO profile | Four nodes × four 32 GiB data disks, login `dba` | Current Go 1.27 passed 16/16 storage, persistence, 20 qcow checks and destroy on Darwin plus Linux native KVM isolation | done for current Tier-1 VM semantics | [`Darwin current`](docs/evidence/m2-private-minio-product-darwin-arm64-dba-go127-20260824.md), [`Linux isolated current`](docs/evidence/m2-private-minio-product-linux-amd64-dba-go127-isolated-20260824.md), [`bare-host Linux history`](docs/evidence/m2-private-minio-product-linux-amd64-20260824.md) |
| Formal guest matrix | Seven guests × two Tier-1 native arches | current UID/GID88 seed, native lifecycle/data/network/time/identity: Linux 7/7 and Darwin 7/7 | done for Tier-1 14/14 | [`current Darwin`](docs/evidence/m4-uid88-guest-refresh-darwin-arm64-go127-20260824.md), [`current Linux five`](docs/evidence/m4-uid88-guest-refresh-linux-amd64-remaining-go127-20260824.md), [`current Linux two`](docs/evidence/m4-uid88-guest-refresh-linux-amd64-el9-d13-go127-20260824.md), [`U24 Darwin`](docs/evidence/m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md) |
| Release packages | Tar/Homebrew/RPM/DEB/checksum/SPDX/Cosign/provenance | current Pigsty-bootstrap source produced byte-identical two-run archives/packages/assembly; isolated Ubuntu/Rocky install/verify/remove passed | in progress | [`current rebuild/package install`](docs/evidence/m4-pigsty-bootstrap-release-go127-20260824.md); production publish not run |

## Required evidence still not run

- Partial-project destroy/recreate and stopped-only private drift reconcile.
- Pigsty full-lab bootstrap; meta deploy/health/stop-start/cleanup passes.
- Tier-2 Darwin amd64 and Linux arm64 native smoke.
- Host reboot persistence on both Tier-1 hosts.
- RPM-family Linux private-network install/lifecycle/uninstall.
- Same-image Lima oracle comparison.
- Production self-host image build/migration output and publication path.
- Real tag workflow, keyless production signature/provenance, Homebrew tap install,
  and published RPM/DEB repository consumption.

## Owner/resource blockers

| Gate | Current fact |
|---|---|
| Image hosting/bandwidth + active/standby manifest-key custody | not assigned |
| Production release identity/custody | workflow exists but has never run and no publisher is assigned |
| Durable macOS arm64 HVF runner | current development Mac is not an owned CI service |

Resolved owner decisions: Quick is 64 GiB + `dba`; every official profile uses
`dba` and the Pigsty wrapper is Piglet-only with no compatibility path; EL8 is outside v1. Darwin host is
the default and shared is evidence-backed; one explicit warned RFC1918 `/24`
override is the collision escape hatch. See ADR-0004, ADR-0008, and ADR-0009.
