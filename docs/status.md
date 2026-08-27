# Status

Farrow is pre-1.0. This page says plainly what has been exercised on real
hardware, what has not, and what stands between here and a 1.0 tag.

A compile is not a boot. The tree currently carries **two generations of
evidence debt**: the historical M0–M4 native runs under `docs/evidence/`
predate both the Farrow namespace transition *and* the product redirection
recorded in [REDESIGN.md](../REDESIGN.md). They establish that the underlying
engine (QEMU lifecycle, transactions, identity, recovery, private networking)
worked end to end; they do not verify the current binary, configuration
format, or drift model.

## Functional baseline (historical, pre-redesign)

Verified natively on macOS arm64 (HVF) and Linux amd64 (KVM) before the
namespace transition:

| Area | |
|---|---|
| Single-VM lifecycle | zero-config up, SSH, exec, outbound network, stop/start persistence, guarded destroy |
| Private `full` lab | fixed `.10`–`.13`, host↔VM and VM↔VM traffic, guest internet over the management NIC, NAT SSH fallback, control-only lateral key |
| Storage | root overlays, data disks, four-node MinIO with 16 disks, persistent-disk preserve/reattach/delete |
| Concurrency | second private project exits 6, 30-cycle create/destroy soak with no leaks |
| Recovery | partial failure, crash and repair paths |
| Privilege | unprivileged QEMU, root-owned helper ownership checks |
| Guest matrix | seven aliases × two native architectures |

## The redesign: implemented and unit-tested, not natively replayed

Everything below builds, passes the unit/race/vet/staticcheck gates, and has
CLI-level functional tests where a VM is not required. None of it has been
replayed on native hardware yet:

- **Inventory-as-config.** The Pigsty-compatible inventory format, the
  `vm_*` namespace, name derivation, defaults, group-conflict rules, and the
  retirement of the `version:`/`nodes:` format.
- **Node-granular lifecycle.** Per-node hashes, additive `up` for
  config-added nodes, lease reshaping, per-node recreate guidance,
  `destroy <node> --force` removal, and the absence-never-destroys rule.
- **Single default mode.** `setup` prepares the fixed-IP lab by default;
  user-mode projects are retired to salvage commands.
- **NetworkManager backend.** The nmcli bridge transaction for the RHEL
  family and NM-owned desktops, firewalld zone assignment, the
  `/etc/farrow/network.json` public identity, and backend-aware preflight,
  doctor, and uninstall. **No native EL run has happened yet.**
- **Orphan governance.** Marker schema 2 (work_dir/name), orphan-aware
  `list`, `project rm`/`project prune`, `destroy --purge`.
- **Verb alignment.** `halt`, `reload`, any-subnet `hosts install`.
- **Setup UX.** Proxy-aware HTTP clients, the single sudo prompt with a
  background credential keeper, the Homebrew socket_vmnet source with
  recorded-digest provenance, and the checklist-style progress output.
  **The Homebrew install path and the sudo keeper have not run against a
  real `brew install` + password prompt end to end.**

## Not verified

- Any native replay of the current tree: single-node and four-node labs on
  both Tier-1 hosts, using the inventory format end to end.
- Scale-out against a **running** lab on native hardware (additive up while
  peers stay up; lease reshape under a live socket_vmnet/bridge).
- The NetworkManager backend on real EL9/Rocky/Alma hardware, including
  firewalld interaction and `network uninstall` prestate restoration.
- A full Pigsty bootstrap (`configure` → `farrow up` → `install.yml`)
  against a Pigsty tree whose conf templates carry `vm_*` variables.
- Opt-in QEMU 9p host shares (wired pre-redesign; still unreplayed).
- Host reboot persistence, on any host.
- Tier-2 native smoke: macOS amd64, Linux arm64.
- Published Homebrew tap, RPM or DEB repository consumption.

## Blocks 1.0

Ownership and infrastructure decisions, unchanged by the redesign:

1. **Production image hosting.** The signed static-repository path is
   implemented, but the public domain, storage owner, and bandwidth are not
   assigned; no image can move from `testing` to `supported`.
2. **Image signing custody.** The catalog verification roots are development
   keys; active and standby custody is not assigned.
3. **Release custody.** The tag workflow exists and is fail-closed but has
   never run; no publisher identity or two-person review is configured.
4. **A durable macOS arm64 runner.** Tier-1 macOS verification still depends
   on a developer machine.

Plus one engineering gate created by the redesign: the **native replay of
the current tree** listed above. Until these close, no `v1.0` tag should be
created, and development artifacts stay explicitly unsigned.

## Known rough edges

- **Restart-class drift is not applied in place.** `vm_cpu`/`vm_mem` changes
  are reported as per-node recreates; the cold-converge path (stop/apply/
  start without rebuilding the root disk) is designed but not implemented.
  `up --restart` is reserved for it.
- **Guest `/etc/hosts` staleness on scale-out.** Existing peers' seeds
  predate a newly added node; Pigsty's `node_etc_hosts` management or a
  per-node recreate covers name resolution of new peers.
- **One active lab at a time.** The host-global lease still binds the whole
  network to one project; address-level leasing is a roadmap item and will
  matter more now that fixed-IP labs are the only mode.
- **Every VM start prints a `testing` image warning** until the hosting and
  custody gates close.

The plan for what comes next is in [phase-2.md](phase-2.md).

## Deliberately out of scope for 1.0

- Cross-architecture emulation. Native only, no TCG fallback.
- Rocky Linux 8 guests (64 KiB-granule arm64 kernel) and EL8 hosts.
- Live QMP snapshots.
- Automatic repair or a global destructive cleanup command — `project prune`
  removes only provable orphans and defaults to a dry run.
