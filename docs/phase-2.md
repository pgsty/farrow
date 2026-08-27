# Phase 2 roadmap

Farrow is pre-1.0. This roadmap records likely work after the current release
gates close; it is not a command reference or a claim that proposed features
exist. [Status](status.md) is the authoritative verification record, and
[REDESIGN.md](../REDESIGN.md) is the product brief this tree implements.

## Current baseline

- one Go CLI and one QEMU backend;
- one Pigsty-compatible inventory as the only configuration format;
- node-granular lifecycle: additive up, per-node recreate, explicit removal;
- an explicit host-global private network with three backends
  (socket_vmnet, systemd-networkd, NetworkManager);
- exactly one deployment per user, all state under `~/.farrow`, nothing
  written into the working directory;
- strict-inside-the-namespace parsing and versioned JSON state;
- bounded Bash-over-SSH provisioning and optional per-node 9p shares.

These invariants continue to apply:

1. QEMU runs as the invoking user.
2. Guest architecture is native and acceleration is mandatory.
3. No arbitrary QEMU argument passthrough or provider/plugin framework.
4. Destructive operations remain ownership-, identity-, and path-bounded;
   configuration absence never implies destruction.
5. Unknown `vm_*` configuration fields fail closed.
6. Privileged components never execute a user-writable binary or shell string.

## Before 1.0

Verification and release ownership, not feature surface:

- replay the current tree natively on both Tier-1 hosts: single-node and
  four-node inventory labs, scale-out against a running lab, per-node
  recreate and removal, `--purge`, and reboot persistence;
- run the NetworkManager backend on real EL9-family hardware, including
  firewalld and uninstall restoration;
- run one full Pigsty bootstrap (`configure` → `farrow up` → `install.yml`)
  once Pigsty's conf templates carry `vm_*` variables;
- verify 9p shares end to end;
- establish image hosting, signing custody, release identity, and a durable
  macOS arm64 runner (see [status.md](status.md)).

## Near-term product work

- **Cold convergence (restart-class drift).** Apply `vm_cpu`/`vm_mem`,
  disk growth, added data disks, and share changes across a stop/start
  cycle without rebuilding the root disk: regenerate the seed and bump the
  node generation. Today these report as per-node recreates.
- **`farrow play`.** The hidden minikube-style playground: one slirp-backed
  singleton VM per user, no configuration file, port-forward UX, flags
  only. The slirp machinery already exists as the management NIC; play is
  porcelain over it (and the natural `pig` integration point).
- **Declarative provisioning.** A `vm_provision` list (script, sudo,
  run-once/always) executed on first boot and by `farrow provision` — a
  bounded hook, not a provisioner framework.

## Post-1.0 proposals

- **Offline snapshots** of stopped VMs (root + non-persistent disks, bound
  to the node hash).
- **Resource admission**: compare declared guest memory with host capacity
  before starting large labs.
- **Better source-sharing performance** while keeping 9p as the portable
  baseline.

## Non-goals

- multiple concurrent deployments per user (projects, registries, or
  address-level leasing) — rejected with the 2026-08-27 simplification, not
  deferred;
- live migration, clustering, or multi-host orchestration;
- a guest agent or general provisioner framework;
- arbitrary QEMU arguments;
- automatic repair or a global destructive cleanup command;
- Windows as a native host platform.
