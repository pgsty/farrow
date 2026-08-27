# Networking

Every Farrow lab runs on one host-global private network: a canonical RFC1918
IPv4 `/24` whose `.1` belongs to the host and whose `.9`–`.254` hold the
nodes' fixed addresses. The host can reach every node by IP, nodes reach each
other, and Ansible connects to the same addresses the inventory declares.
There is no mode to choose; `farrow setup` prepares this network and
`farrow up` uses it.

## Address layout

| Address | Role |
|---|---|
| `.1` | host |
| `.2`–`.8` | DHCP boundary and valid guest L2 VIPs at `.2`–`.4` |
| `.9`–`.254` | static node addresses |

The subnet is derived from the inventory's host addresses. For a generated
template, setup starts with `10.10.10.0/24`; if that collides it probes
`172.31.251.0/24`, `172.31.252.0/24`, `192.168.250.0/24`, and
`192.168.251.0/24`, and writes the winner into the generated file so host and
guests always share one decision. An explicit configuration is never silently
rebased.

## Two NICs per node

Every node has two network interfaces:

- **management** — QEMU user-mode NAT (slirp): DHCP, DNS, the default route,
  outbound internet, and a loopback SSH fallback;
- **private** — deterministic MAC, the fixed inventory address, no default
  route, no DNS, no IPv6 router advertisement.

This is the classic Vagrant topology (NAT eth0 + host-only eth1), and it
means guest→internet traffic crosses the user-mode stack. That stack tops out
around 1–2 Gbit/s per stream and burns a host core while at it — usually
invisible behind a ≤1 Gbit/s uplink, and structurally minimized by Pigsty
itself: after the bootstrap download, nodes install from the local repo on
the control node over the private NIC at full speed. On faster uplinks, point
`proxy_env` at a host-side proxy or serve offline packages from the host
(`.1`) — both paths ride the private NIC. Host↔guest and guest↔guest traffic
never touches slirp.

Private networking never silently degrades: if the fixed-IP path is not
ready, the lifecycle operation fails instead of pretending the NAT NIC is
equivalent.

## What setup does

```bash
farrow setup                # prepare host + single-node lab
farrow setup full           # 4-node template
farrow setup --dry-run      # show the transaction without applying it
```

Setup combines dependency installation, subnet selection, network
installation, hosts-helper verification, config generation, and final
verification into one idempotent transaction. Existing state is handled
conservatively:

| State found by setup | Decision |
|---|---|
| no private installation, requested subnet free | install it |
| healthy Farrow installation matches | reuse it; no host mutation |
| healthy installation on another subnet, profile newly generated | rebase the generated profile to it |
| default subnet collides, profile newly generated | try the bounded free-subnet candidates |
| explicit config conflicts with the installed network | preserve both; report how to align |
| foreign route/interface/service owns the subnet | never adopt or delete it |
| partial, invalid, or ownership-unsafe installation | preserve it; report the failed invariant |
| lease held by another project | preserve it; identify the project to stop |

There is deliberately no automatic "delete whatever is there" path.

## macOS backend

Private networking uses a pinned upstream socket_vmnet daemon running from a
root-owned location; QEMU itself stays unprivileged (Apple's vmnet API
requires root or a restricted entitlement, so *some* root component is
physically unavoidable on macOS — this is the same constraint VirtualBox
solves with a kext and OrbStack with a privileged helper).

Setup downloads the release pinned in the binary, verifies its SHA-256 and
archive structure, generates one persistent interface UUID, installs the root
service, and records the exact BSD interface it created in protected identity
markers. Preflight accepts only that interface; a foreign VirtualBox,
OrbStack, Internet Sharing, or VPN interface holding `.1/24` is never
adopted.

The default vmnet mode is `host` — since internet rides the management NIC,
the private NIC needs no second NAT, and host mode avoids contending with
other vmnet consumers (`VMNET_SHARING_SERVICE_BUSY (1009)` names such a
conflict). `farrow setup --mode shared` selects shared mode explicitly.

## Linux backends

The bridge follows whichever network manager owns the host:

- **NetworkManager active** (the RHEL family, most desktops): Farrow creates
  one owned `farrow0` bridge connection via `nmcli` — manual `.1/24`, IPv6
  disabled, autoconnect, and `connection.zone trusted` on firewalld hosts. It
  never starts systemd-networkd, so the dormant-networkd wireless hazard does
  not exist on these hosts. A world-readable identity file at
  `/etc/farrow/network.json` lets read-only preflight verify the install.
- **systemd-networkd active**: the original transaction — owned
  `.netdev`/`.network` units for `farrow0`.
- **neither active, networkd installable**: Farrow proves that starting the
  dormant networkd cannot claim any real host link before activating it. An
  ambiguous Wi-Fi or Ethernet match is a hard stop.

Both backends share the rest: the distribution `qemu-bridge-helper` (with
reversible `dpkg-statoverride` on Debian-family; the packaged setuid helper
verified on RPM), the marker-owned `/etc/qemu/bridge.conf` block, the lease
boundary under `/run/farrow`, a root-owned ownership manifest, and a
non-root QEMU attach smoke before anything persists. Switching backends
requires an explicit `farrow network uninstall --yes`.

The private bridge runs no DHCP server — node addresses are injected by
cloud-init, and guest internet comes from the management NIC.

## One lab at a time

The installer owns `/private/var/run/farrow` (macOS) or `/run/farrow`
(Linux) and stores a strict lease there. One active project may use the
network at a time; a second exits 6. Growing a running lab reshapes the
lease (new reservations added, surviving ones untouched); destroying a node
shrinks it. Address-level leasing for multiple concurrent labs is a roadmap
item.

## Publishing names

`farrow hosts install` writes the project's node aliases (`vm_alias`) into
`/etc/hosts` through the root-owned digest-pinned helper, as one marker-owned
block. It accepts any RFC1918 static address, prints its exact plan, and
changes nothing without `--yes`. Inside the guests, the same aliases are part
of each node's seed.

For zero-prompt hosts updates, an administrator may add a narrow sudoers
rule for exactly the helper — this is why it is a separate root-owned binary
rather than the user-writable CLI:

```text
%admin ALL=(root) NOPASSWD: /opt/farrow/libexec/farrow-hosts-helper
```

## Advanced inspection

```bash
farrow network preflight -f pigsty.yml --json   # read-only; exact node addresses
farrow network status --json
farrow network install --yes                     # manual transaction (review first)
farrow network uninstall --yes                   # restores recorded prestate
```

Preflight runs internally before setup installation and every private
lifecycle mutation. It detects mismatched installation state, overlapping
routes/interfaces, static addresses that already answer SSH, partial or
unsafe ownership, an unready backend, and macOS vmnet error 1009. Uninstall
refuses while a lease is active and restores the recorded prior host state.
