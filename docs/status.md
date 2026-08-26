# Status

Farrow is pre-1.0. This page says plainly what has been exercised on real
hardware, what has not, and what stands between here and a 1.0 tag.

A compile is not a boot. A fake QMP server is not an accelerator. The entries
marked **verified** below were run end to end on native hosts before the Farrow
namespace transition. They establish the functional baseline, but do not by
themselves verify the renamed binary, paths, environment, bridge and packages.

## Functional baseline verified on both Tier‑1 hosts

macOS arm64 with HVF, and Linux amd64 with KVM.

| Area | |
|---|---|
| Quick VM | zero-config `up`, SSH, `exec`, outbound network, resolved CPU/memory/disk, default `/data`, stop/start persistence, port re-selection, per-project host-key pinning, guarded destroy |
| Private `full` lab | fixed `.10`–`.13`, host↔VM and VM↔VM traffic, guest internet over the management NIC, no default route or DNS on the private NIC, NAT SSH fallback, control-only lateral key |
| Storage | 64 GiB root plus ordinary data disks, four-node MinIO with 16 disks, persistent-disk preserve/reattach/delete |
| Concurrency | second private project exits 6, 30-cycle create/destroy soak with no leaks |
| Recovery | partial failure, crash and repair paths |
| Privilege | unprivileged QEMU, root-owned helper ownership checks |
| Guest matrix | seven aliases × two native architectures |
| Source gates | unit, race, vet, staticcheck, govulncheck, four-target cross-build |
| Reproducibility | development archives and Linux packages build byte-identical across runs |

## Partially verified

| Area | Gap |
|---|---|
| Farrow namespace transition | CLI output unit, race and vet checks pass; whole-tree gates plus post-rename native Quick/private and packaging replay remain pending |
| Quick negative paths | no dedicated native port-collision injection; `--no-data-disk` not separately run on Linux |
| Loopback forwards | all four endpoints exercised on Linux, not on macOS |
| Private node selectors | unit and integration coverage complete; native partial recreate not run |
| `full` and `minio` profiles | topology and storage verified before the current UID/GID 88 seed; identity refresh not rerun |
| Linux custom subnet | preflight and dry plan verified; privileged apply not run on a disposable host |
| Image pipeline | local validation, rejection and reproducibility verified with real `qemu-img`; libguestfs normalization not run natively |

## Not verified

- A post-rename native replay using the Farrow binary, paths, environment,
  bridge identity and package names. Historical evidence remains immutable
  under `docs/evidence`; new Farrow evidence must come from a fresh run.
- Opt-in QEMU 9p host shares. The source path is wired for Quick and Private,
  but no build or native Quick/four-node replay has been run after the change.
- Host reboot persistence, on either Tier‑1 host.
- Tier‑2 native smoke: macOS amd64, Linux arm64.
- Private network install on an RPM-family Linux host.
- Full Pigsty lab bootstrap. Single-node `meta` bootstrap and deploy pass.
- Published Homebrew tap, RPM or DEB repository consumption.

## Blocks 1.0

These are ownership and infrastructure decisions, not code:

1. **Image hosting.** The manifest currently points at upstream distribution
   URLs. Self-hosted object storage, a domain and bandwidth are not assigned,
   so no image can move from `testing` to `supported`.
2. **Image signing custody.** The manifest verification roots in the binary are
   development keys whose private halves exist only in tests. Active and
   standby custody is not assigned.
3. **Release custody.** The tag workflow exists and is fail-closed, but has
   never run. No remote, production tag, publisher identity or two-person
   review is configured.
4. **A durable macOS arm64 runner.** Tier‑1 macOS verification currently
   depends on a developer machine rather than an owned CI host.

Until all four are resolved and the unverified list above is closed, no `v1.0`
tag should be created, and development artifacts must stay explicitly unsigned
and unattested.

## Known rough edges

- **Linux private network on a wireless host.** `network install` refuses to
  start a dormant `systemd-networkd` when any existing `.network` file could
  claim a real link. Since systemd itself ships `80-wifi-adhoc.network`, every
  host with a wireless interface and dormant networkd hits this and must either
  adopt networkd deliberately or stay on quick mode. The refusal is correct and
  fail-closed, but it is the first thing most Linux desktop users will hit.
- **`doctor` exits 3 on a host without the private network installed**, because
  private-network readiness is part of its verdict. Quick mode is unaffected,
  but the exit code reads as a broken host until you know that.
- **Every VM start prints a `testing` image warning.** It will stay until the
  hosting and custody gates close.

The plan for what comes after these close is in
[phase-2.md](phase-2.md).

## Deliberately out of scope for 1.0

- Cross-architecture emulation. Native only, no TCG fallback.
- Rocky Linux 8. Its arm64 image uses a 64 KiB-granule kernel that will not
  boot on Apple silicon HVF.
- Standalone partial `destroy`. A private project is destroyed whole.
- In-place reconcile of a stopped private project. Any desired-state change is
  an explicit destructive recreate.
- Any global cleanup command.
