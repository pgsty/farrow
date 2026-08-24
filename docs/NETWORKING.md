# Networking

## Quick/user mode

Quick is one VM with QEMU user-mode NAT. It needs no privilege, helper, Lima,
libvirt, VirtualBox, or host bridge. DHCP/DNS/default route and internet come
from slirp. SSH and all explicit forwards bind loopback by default.

The quick defaults are node `meta`, image `u24`, 2 CPUs, 4 GiB RAM, 64 GiB root,
and a 64 GiB sparse `/data` disk. The active execution prompt sets login user
`dba`. Business forwards are 15432:5432, 13000:3000, 18080:80, and 18443:443.
Each preferred port tries `preferred + n*10000` for `n=0..4`; the chosen value
is persisted in the resolved spec.

User networking does not promise host access to the guest DHCP address or full
ICMP behavior.

## Private lab

Private mode is one host-global RFC1918 IPv4 `/24` and one active project. The
default is `10.10.10.0/24`; host is `.1`, DHCP fallback ends at `.8`, and
static addresses are `.9-.254`. `.9` remains available for existing build/VIP
behavior and `.2-.4` remain valid guest L2 VIPs.

`piglet network preflight` is read-only and runs independently or automatically
before install and private lifecycle mutation. It detects installed-network
mismatch, broad/narrow route overlap, interface overlap, requested static
addresses accepting SSH, partial/unsafe ownership, missing backend readiness,
and Darwin vmnet 1009. Stable exit classes are usage 2, capability 3, state 4,
resource 6, and integrity 7.

An explicit custom `/24` is the escape hatch, never an automatic random choice.
`init PROFILE --network-cidr` preserves node suffixes and emits a warning.
Installed network, resolved spec, state, lease, host `.1`, DHCP `.8`, and every
node must match. Existing arbitrary YAML is not silently remapped; CIDR changes
require stop/no lease and network uninstall→install.

Pigsty integration uses the same selected layout. `pigsty-vm inventory`
rebases catalog-bound inventory host keys, admin addresses, L2 VIPs, service
references, DNS/hosts/NTP entries, and endpoints while preserving final
octets. It adds a custom `/24` to `proxy_env.no_proxy`, rejects residual or
unclassified default-subnet semantics, and never modifies source `conf/`.
Managed inventory is mode 0600 with an ownership/hash sidecar.

Each VM has two NICs:

- management: user NAT, DHCP, DNS, default route, internet, SSH fallback;
- private: deterministic MAC, static address, no default route, no DNS.

Private failure is fatal and never falls back to user mode. Acceptance requires
real host-to-VM, VM-to-VM, and VM-to-internet checks.

## Host-global private lease

The network installer owns the sticky runtime root selected by ADR-0006:
`/private/var/run/piglet` on macOS and `/run/piglet` on Linux. Private projects
never place the global lease beneath a configurable project data root. The
strict lease serializes one active project, records UID/network/IP/MAC/QMP/
process identity, and makes changed or second-project reservations conflicts.
Release/reclaim requires an identity audit; cross-UID stale replacement is not
an unprivileged operation.

## macOS

A pinned upstream `socket_vmnet` daemon runs as root from a root-only path;
QEMU remains unprivileged. Product v1 prefers host mode and accepts shared when
the selected global subnet passes real startup/reachability checks. QEMU stream
plus probed reconnect spelling is preferred; Go dial plus `ExtraFiles` and
`socket,fd=3` is the runtime fallback. The client binary is diagnostics only.

Native arm64 host-mode evidence now verifies `10.10.10.1`, DHCP end `.8`, two
UID-501 QEMU VMs, host→VM, VM→VM, management internet, and stream reconnect
after launchd daemon restart with the same persistent interface UUID. Public
plan/install/repeated verification/active-lease guard/uninstall/fresh reinstall
passed with host mode, the pinned archive, and persistent UUID; see
[`m2-darwin-network-product-executor-20260824.md`](evidence/m2-darwin-network-product-executor-20260824.md).

### Shared mode, subnet conflicts, and the corrected decision

Apple documents shared mode as a NAT-backed interface that should reach the
Internet, host, and other shared interfaces. socket_vmnet exposes the same
vmnet mode plus gateway/DHCP-range controls. Piglet's exact topology is two
dual-NIC guests, static `.10/.11` outside DHCP `.2-.8`, and management user-NAT
alongside the private NIC.

The first shared run appeared to disprove that topology, but its neighbor table
contained stale MACs from stopped projects and it did not snapshot VirtualBox
or other vmnet consumers. A later controlled run found the decisive error:
`vmnet_start_interface` returned `1009`, which the local Apple SDK defines as
`VMNET_SHARING_SERVICE_BUSY`. A listening Unix socket existed even though no
host `.1` or `/24` route had been created, so the old installer/status check
was a false positive.

A second ownership audit found another unsafe edge in address-only discovery:
a foreign `vboxnet0` (or any other interface) at the expected `.1/24` could be
mislabeled as Piglet's installed interface. Fresh install now baselines exact
matches before launch, requires exactly one newly created match, and persists
its BSD name with the vmnet UUID/CIDR/host in a root-owned public marker plus a
byte-identical root-only twin. Preflight accepts only that marker-bound name;
foreign exact interfaces remain resource conflicts. See
[`m2-darwin-interface-ownership-preflight-20260824.md`](evidence/m2-darwin-interface-ownership-preflight-20260824.md).

The root audit found `com.apple.NetworkSharing` active and
`com.apple.vmnet.plist` holding `Host_Net_Address = 10.10.10.1/24`, including
stale/active Piglet UUID MAC allocations. That proves the Apple sharing-state
collision, but not which application originally created it. A VirtualBox guest
using `10.10.10.10` or a host-only interface on that `/24` is a fully plausible
cause and is now detected as foreign rather than adopted. OrbStack's visible
networks used different ranges, so it was another candidate vmnet consumer,
not a proven owner of `10.10.10.0/24`.

With the same pinned binary and other virtualization still running, host mode
started successfully on `172.31.250.0/24` and shared started successfully on
`172.31.251.0/24`. Shared initially injected IPv6 RA DNS on `private0`; the
guest renderer now sets `accept-ra: false` and `link-local: []`. The corrected
two-node shared run then passed every private contract check:

| Layer | Clean alternate-subnet shared result |
|---|---|
| vmnet host `.1` and `/24` route | pass |
| QEMU/socket/virtio attachment | pass, UID 501 |
| Guest static `.10/.11` and private route | pass |
| No private default route or DNS | pass |
| Host → both guests | pass |
| `.10` → `.11` ICMP and lateral SSH | pass |
| Internet via separate management NIC | pass |

See [`m0-b-darwin-shared-clean-subnet-20260824.md`](evidence/m0-b-darwin-shared-clean-subnet-20260824.md).
The old negative bundle remains useful only as a record of a contaminated
failure, not as evidence that shared is intrinsically unsuitable.

Alternatives:

1. **Default:** socket_vmnet host mode for the static L2 private network, plus
   QEMU user-NAT management for DNS/default route/Internet.
2. **Valid fallback:** socket_vmnet shared mode on a proven-free subnet, while
   the guest private NIC still has no default route, DNS, DHCP, or IPv6 RA.
3. If another vmnet consumer owns `10.10.10.0/24`, stop that consumer or make
   one explicit host-global subnet change that moves host, DHCP boundary, all
   guest addresses, lease, and network state together. Never change only the
   daemon subnet or silently pick a random range.
4. Bridged mode delegates addressing/security to the physical LAN and cannot
   guarantee the global `10.10.10.0/24` lab, so it is not the default private
   backend.
5. QEMU's built-in vmnet would require the whole QEMU process to run as root;
   this violates Piglet's privilege boundary.
6. Loopback forwards can expose individual services but cannot replace peer
   L2 traffic, VIPs, or the fixed-IP lab contract.

Upstream references: [Apple vmnet modes](https://developer.apple.com/documentation/vmnet),
[socket_vmnet static addressing](https://github.com/lima-vm/socket_vmnet#how-to-use-static-ip-addresses),
and [socket_vmnet/QEMU privilege comparison](https://github.com/lima-vm/socket_vmnet#how-is-socket_vmnet-related-to-qemu-builtin-vmnet-support).

## Linux

The root-owned persistent bridge is `piglet0` at `10.10.10.1/24`. QEMU uses a
validated `qemu-bridge-helper` while remaining unprivileged. Debian/Ubuntu
permission changes use reversible `dpkg-statoverride`; RPM hosts are reported,
not mutated. Only Piglet marker blocks in bridge and NetworkManager config may
be changed.

ADR-0007 selects systemd-networkd as the narrow v1 persistence boundary. A
clean Ubuntu 24.04 NetworkManager-only host is supported by recording the exact
prestate of `systemd-networkd.service`, its socket, network generator, and
wait-online unit. Before changing the host, Piglet proves that the effective
networkd configuration cannot match any existing non-Piglet link or the planned
bridge; non-Piglet netdevs, drop-ins, mutable/unreadable files, and unsupported
matching syntax are refused. Piglet then loads its dedicated NM unmanaged rule
before creating `piglet0`, starts networkd non-persistently, and enables it only
after non-root helper attach succeeds.

The plan includes exact netdev/network units, marker-only bridge.conf,
root-owned shared lease lock/runtime root, strict content hashes, and an
ownership manifest that distinguishes absent from pre-existing paths. Native
Ubuntu evidence completed install → two KVM VMs → repeated snapshot →
uninstall, restoring helper root:root 0755 and all four networkd units to their
original disabled/inactive state with no Piglet host-network residue. The
public ordered executor subsequently passed the same clean-host cycle via
`piglet network install [--yes]` and `uninstall [--yes]`, including a non-root
QEMU/QMP helper-attach smoke before persistence, repeated install snapshot
preservation, and active-lease uninstall refusal. See
[`m2-linux-network-product-executor-20260824.md`](evidence/m2-linux-network-product-executor-20260824.md).
