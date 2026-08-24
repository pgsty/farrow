# ADR-0007: Linux v1 private bridge persistence via systemd-networkd

Status: accepted for the M0 Linux persistence mechanism after native Ubuntu
24.04/KVM install/E2E/uninstall; product executor and M2 gates remain.

## Context

Linux private mode needs a persistent `piglet0`, reversible bridge-helper
permission handling, and no root QEMU. Supporting arbitrary combinations of
NetworkManager, networkd, distro scripts, and ad-hoc capabilities would make
the privileged surface unauditable.

## Decision

The current v1 plan supports systemd hosts and may start/persist networkd on a
NetworkManager-only host only after recording the exact prestate of:

```text
systemd-networkd.service
systemd-networkd.socket
systemd-network-generator.service
systemd-networkd-wait-online.service
```

Masked, failed, unknown, or otherwise unsupported unit states are refused. The
ordered transaction installs exact root-owned files:

```text
/etc/systemd/network/80-piglet0.netdev
/etc/systemd/network/80-piglet0.network
/etc/qemu/bridge.conf                 # exact Piglet marker block only
/etc/tmpfiles.d/piglet.conf           # root:root 1777 /run/piglet
/run/piglet/private-lease.lock        # root:root 0666 under sticky root
/var/lib/piglet/network.json          # ownership/rollback manifest
```

If NetworkManager is also active, only a dedicated Piglet unmanaged drop-in is
added and reloaded before the bridge can appear. Before any host mutation, an
inactive-networkd install inventories the effective `.network`/`.netdev` files
and current link names, alternative names, kinds, and types. It refuses any
non-Piglet netdev or drop-in, never uses unsupported match predicates as proof,
and refuses any network file that is not provably disjoint from every existing
non-Piglet link and the planned `piglet0`. Only then may networkd start without
being enabled; install validates `piglet0`/`.1`, then applies the helper
boundary. Networkd persistence is enabled only after a non-root QEMU attach
succeeds. Existing unowned
`piglet0`, unmarked `allow piglet0`, modified marker
blocks, or changed owned-file hashes are hard conflicts.

On Debian-family hosts, a helper that is not already root:kvm 4750 may be
changed only when no non-Piglet `dpkg-statoverride` exists. The manifest records
the original owner/group/mode; uninstall removes the exact override and
explicitly restores that state. RPM-family hosts are never mutated: the
distribution-owned root mode-4755 helper is accepted with an explicit
multi-user warning, and any other mode is unsupported.

The manifest distinguishes absent bridge.conf/`/etc/qemu` from pre-existing
content and metadata, preserves the original helper and unit snapshot across
repeated install, and uses a fixed file allowlist. Uninstall requires no active
private lease, QEMU, or bridge member, exact content hashes, and matching
helper/override state. It deletes the bridge while NM still treats it as
unmanaged, restores helper and unit state, removes state last, and uses only
`rmdir` for directories proven to have been created by Piglet and empty.

## Consequences

- QEMU remains unprivileged and receives only typed
  `-netdev bridge,br=piglet0,helper=<validated path>`.
- NetworkManager-only Ubuntu hosts are within the boundary only when the
  recorded four-unit transaction and the pre-mutation activation proof both
  pass. Stock vendor rules remain compatible when their positive Name/Type
  predicates prove they cannot match the host links; arbitrary, mutable, or
  ambiguous networkd configuration fails closed.
- Native evidence on `vonng-aimax` completed clean install, UID-1000 helper
  attach, two KVM VMs with three-way reachability, repeated snapshot, and
  uninstall. All four units returned to disabled/inactive, helper returned to
  root:root 0755, and no bridge/route/config/override/state/lock/directory
  residue remained. See
  [`m0-c-linux-amd64-native-20260824.md`](../evidence/m0-c-linux-amd64-native-20260824.md).
- The public executor later passed plan/install/repeated-install/status,
  non-root QEMU/QMP attach, active-real-lease uninstall refusal, and final
  uninstall/no-residue. See
  [`m2-linux-network-product-executor-20260824.md`](../evidence/m2-linux-network-product-executor-20260824.md).
  Reboot, RPM native evidence, transaction crash injection, and M2 soak remain.
