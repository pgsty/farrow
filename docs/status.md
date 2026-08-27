# Status

Farrow is pre-1.0. This page says plainly what has been exercised on real
hardware, what has not, and what stands between here and a 1.0 tag.

> **2026-08-27 evening simplification.** After this page was written, the
> tree removed the project concept (one global deployment per user), removed
> the private-network lease, moved the image store under `~/.farrow/images/`,
> and made `farrow.yml` the preferred configuration name. Every native-replay
> claim recorded before that evening was exercised against machinery that no
> longer exists in that form; the affected capabilities below are **pending
> re-verification** on the simplified tree, not verified.

A compile is not a boot. The tree currently carries **three generations of
evidence debt**: the historical M0–M4 native runs under `docs/evidence/`
predate the Farrow namespace transition *and* the product redirection
recorded in [REDESIGN.md](../REDESIGN.md), and everything predates the
2026-08-27 simplification above. They establish that the underlying
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
| Concurrency | 30-cycle create/destroy soak with no leaks (the lease arbitration exercised then has since been removed outright) |
| Recovery | partial failure and crash paths (the `repair` command exercised then has since been removed) |
| Privilege | unprivileged QEMU, root-owned helper ownership checks |
| Guest matrix | seven aliases × two native architectures |

## The redesign: implemented and unit-tested, not natively replayed

Everything below builds, passes the unit/race/vet/staticcheck gates, and has
CLI-level functional tests where a VM is not required. None of it has been
replayed on native hardware yet:

- **Inventory-as-config.** The Pigsty-compatible inventory format, the
  `vm_*` namespace, name derivation, defaults, group-conflict rules, and the
  retirement of the `version:`/`nodes:` format. `farrow.yml` is now the
  preferred name; `pigsty.yml` remains fully supported.
- **Node-granular lifecycle.** Per-node hashes, additive `up` for
  config-added nodes, per-node recreate guidance, `destroy <node> --force`
  removal, and the absence-never-destroys rule.
- **Single default mode.** `setup` prepares the fixed-IP lab by default;
  the user-mode (slirp) lifecycle is deleted entirely.
- **One global deployment.** The 2026-08-27 simplification: no workspace
  marker, no project registry, no lease — one deployment per user under
  `~/.farrow`, arbitrated by state plus a runtime identity audit. The
  `list`/`repair`/`project *`/`debug bundle`/`network preflight` commands
  are gone. Unit-tested only; nothing here has run natively.
- **NetworkManager backend.** The nmcli bridge transaction for the RHEL
  family and NM-owned desktops, firewalld zone assignment, the
  `/etc/farrow/network.json` public identity, and backend-aware readiness
  probes, doctor, and uninstall. **No native EL run has happened yet.**
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
  peers stay up, under a live socket_vmnet/bridge).
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
- **Guest `/etc/hosts` staleness on scale-out.** Existing peers' seeds
  predate a newly added node; Pigsty's `node_etc_hosts` management or a
  per-node recreate covers name resolution of new peers.
- **Interrupted transitions converge through `farrow status`.** A node left
  in `stopping`/`starting`/`destroying` by a killed CLI is audited on the
  next `status`: a provably dead runtime converges to `stopped` (then
  `start`/`destroy` work); a live one shows as `running` and `farrow stop`
  finishes the transition. This replaced the removed `repair` command; only
  an identity-ambiguous live runtime still requires manual inspection.
- **Every VM start prints a `testing` image warning** until the hosting and
  custody gates close.

The plan for what comes next is in [phase-2.md](phase-2.md).

## Deliberately out of scope for 1.0

- Cross-architecture emulation. Native only, no TCG fallback.
- Rocky Linux 8 guests (64 KiB-granule arm64 kernel) and EL8 hosts.
- Live QMP snapshots.
- Automatic repair or a global destructive cleanup command — `image prune`
  removes only provably unreferenced files and defaults to a dry run, and
  `destroy` stays ownership- and path-bounded.
