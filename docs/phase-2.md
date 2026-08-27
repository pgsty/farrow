# Phase 2 roadmap

Farrow is still pre-1.0. This roadmap records likely work after the current
release gates close; it is not a command reference or a claim that proposed
features already exist.

[Status](status.md) is the authoritative verification record. Most native
evidence under `docs/evidence/` predates the Farrow namespace and remains a
historical functional baseline. A fresh Farrow-native Quick/private replay,
package replay, and the other checks listed in `status.md` are still required.

## Current baseline

- one Go CLI and one QEMU backend;
- zero-privilege Quick mode and an explicit host-global private network;
- strict YAML input and versioned JSON state;
- guarded lifecycle, repair, deletion, image, and network operations;
- bounded Bash-over-SSH provisioning with `farrow provision`; and
- optional per-node QEMU 9p host shares.

The 9p configuration, resolved intent, inherited directory descriptor, QEMU
device, and guest mount path are implemented in source. They are not yet
promoted to the verified baseline: a fresh build and native Quick/four-node
round-trip on both Tier-1 hosts remain pending.

These invariants continue to apply:

1. QEMU runs as the invoking user; Quick needs no privilege.
2. Guest architecture is native and acceleration is mandatory.
3. No arbitrary QEMU argument passthrough or provider/plugin framework.
4. Destructive operations remain ownership-, identity-, and path-bounded.
5. Unknown configuration fields fail closed.
6. Privileged components never execute a user-writable binary or shell string.

## Before 1.0

The immediate work is verification and release ownership, not a broader feature
surface:

- replay the Farrow binary, paths, environment, network identity, packages, and
  9p shares on the Tier-1 hosts;
- establish self-hosted image storage and artifact-retention policy;
- assign active and standby image-signing custody;
- establish release identity and two-person publication review; and
- provide a durable macOS arm64 runner.

No image should move from `testing` to `supported`, and no `v1.0` tag should
be created, until the corresponding gates in `status.md` are closed.

## Near-term product work

- **Verify and finish 9p shares.** Require read-only and read-write round-trips
  on both Tier-1 hosts, correct `dba` ownership, stop/start persistence,
  multi-node coverage, and fail-closed handling of a missing source path.
- **Improve recovery guidance.** When an error has one safe remedy, text and
  structured output should name it without introducing automatic repair.
- **Check image freshness.** Monitor every embedded URL and document refresh
  through the existing normalization pipeline; dated upstream URLs are not a
  durable distribution channel.

## Post-1.0 proposals

Everything in this section is a proposal. The command shapes are illustrative
and are not implemented.

### Offline snapshots

Use stopped-VM qcow2 operations only. A snapshot would cover root and
non-persistent data disks and bind to the resolved-spec hash.

```bash
# proposal; not implemented
farrow snapshot create <name>
farrow snapshot list
farrow snapshot restore <name> --force
farrow snapshot delete <name> --force
```

Live QMP snapshots and implicit inclusion of persistent disks remain out of
scope.

### Single-node reset

A private lab could eventually remove and recreate one selected node while
retaining the lease and leaving peers untouched.

```bash
# proposal; not implemented
farrow destroy --force meta-1
```

### Project garbage collection

Destroyed project metadata should be removable only when no node artifact,
retained disk, matching process, or private lease remains.

```bash
# proposal; not implemented
farrow project prune --dry-run
farrow project prune --yes
```

### Resource admission

`plan` and `up` should compare declared guest memory with host capacity before
starting large profiles such as `simu`. Any overcommit override would be
explicit; no such flag exists today.

### Multiple concurrent private labs

Moving beyond one host-global private lease requires a separate design review.
The preferred direction is a small, hard-capped set of subnet leases rather
than multiplying privileged bridges and daemons.

## Later platform work

- improve systemd-networkd matching so unrelated wireless configuration does
  not create avoidable false positives;
- decide whether explicitly labelled cross-architecture emulation belongs in
  the product at all; and
- keep NetworkManager-owned private networking unsupported until it has an
  equally bounded install, ownership, and rollback model.

## Non-goals

- live migration, clustering, or multi-host orchestration;
- a guest agent or general provisioner framework;
- arbitrary QEMU arguments;
- automatic repair or a global cleanup command; and
- Windows as a native host platform.

## Order

| Order | Work | Gate |
|---|---|---|
| 1 | Farrow-native replay, 9p verification, release and image custody | before 1.0 |
| 2 | recovery guidance and image freshness | independently after baseline replay |
| 3 | snapshots, node reset, and project pruning | post-1.0 design review |
| 4 | admission control and concurrent private labs | separate safety review |
