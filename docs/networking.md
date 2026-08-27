# Networking

Farrow has two network modes:

- **Quick** uses QEMU user NAT. It needs no host network and no privilege.
- **Private** gives one or more nodes fixed addresses on one host-global `/24`.

The supported entry point for either mode is `farrow setup`; the low-level
`farrow network ...` commands later in this document are for diagnosis and
explicit host-network administration.

## What setup does

```bash
farrow setup        # Quick
farrow setup meta   # private, one node
farrow setup full   # private, four nodes
```

Setup combines dependency installation, subnet selection, network
installation, config publication, and final verification into one idempotent
transaction. The next command in all three cases is `farrow up`.

Private setup also locates the helper shipped beside the CLI (or in a formula's
`libexec`), verifies it against the digest embedded in that CLI, and installs
that exact companion at `/opt/farrow/libexec/farrow-hosts-helper`. A missing,
mismatched, or pre-existing unsafe privileged helper is preserved and blocks
setup instead of being replaced blindly.

For a generated private profile, setup starts with `10.10.10.0/24`. If that
subnet is unavailable and no explicit choice or user config would be changed,
it probes a bounded set of alternatives: `172.31.251.0/24`,
`172.31.252.0/24`, `192.168.250.0/24`, and `192.168.251.0/24`. The subnet that
passes preflight is written into `farrow.yaml`, so the host and guests always
share one decision.

The private network itself supports these custom subnets. The optional
`farrow hosts install` publisher currently accepts only addresses in the
default `10.10.10.0/24`; use SSH config or DNS for aliases on another `/24`.

Existing state is handled as follows:

| State found by setup | Decision |
|---|---|
| no private installation, requested subnet is free | install it |
| healthy Farrow installation matches | reuse it; no host mutation |
| healthy Farrow installation uses another subnet and the profile is newly generated | rebase the generated profile to that installed subnet |
| default subnet collides and the profile is newly generated | try the bounded free-subnet candidates above |
| `--network-cidr` or `-f` conflicts with the installed network | preserve both; stop and report how to align the explicit config or replace the old installation |
| foreign route/interface/service owns the subnet | never adopt or delete it; use a free subnet or stop the named owner |
| partial, invalid, or ownership-unsafe Farrow installation | preserve it; report the exact failed invariant and bounded repair/uninstall route |
| matching network has a lease held by the current project | reuse it without disrupting that project |
| matching network has a lease held by another project | preserve it; report the project via `farrow list --json` and require that project to stop first |
| changing or uninstalling a network with an active lease | refuse; stop/destroy the owning project and release the lease first |

There is deliberately no automatic “delete whatever is there” path. Replacing
host-global networking can interrupt another VM, VPN, or user, so setup solves
free choices automatically and stops only at an ownership or destructive
boundary.

`farrow setup --dry-run` shows the transaction without applying it.
`farrow setup ... --yes` accepts the single transaction in automation.

## Quick mode

One VM sits behind QEMU's user-mode network stack. DHCP, DNS, the default route,
and outbound internet all come from slirp. No bridge, helper, daemon, or sudo is
involved.

Services reach the guest through loopback forwards:

| Host | Guest | Service |
|---|---|---|
| `127.0.0.1:2222` | `22` | SSH |
| `127.0.0.1:15432` | `5432` | Postgres |
| `127.0.0.1:13000` | `3000` | Grafana |
| `127.0.0.1:18080` | `80` | HTTP |
| `127.0.0.1:18443` | `443` | HTTPS |

If a preferred port is busy, Farrow tries `preferred + n × 10000` for
`n = 1..4` and stops. The selected port is recorded in the resolved spec and
printed by `status`.

Add or replace forwards at `up` time:

```bash
farrow up --forward 6432:6432 --forward 0.0.0.0:8080:80
farrow up --no-default-forwards --forward 5432:5432
```

User NAT does not give the host a route to the guest's DHCP address or full
ICMP behaviour. A loopback forward is reachable by other local users on the
same machine; loopback blocks the LAN, not neighbouring users on the host.

## Private mode

A private project uses one canonical RFC1918 IPv4 `/24`:

| Address | Role |
|---|---|
| `.1` | host |
| `.2`–`.8` | DHCP range and valid guest L2 VIPs at `.2`–`.4` |
| `.9`–`.254` | static node addresses |

Every node has two NICs:

- **management** — user NAT for DHCP, DNS, default route, internet, and an SSH
  fallback on loopback;
- **private** — deterministic MAC, static address, no default route, no DNS,
  and no IPv6 router advertisement.

Private networking never silently degrades. If the fixed-IP path is not ready,
the lifecycle operation fails instead of pretending the NAT NIC is equivalent.

### One private lab at a time

The installer owns `/private/var/run/farrow` on macOS or `/run/farrow` on Linux
and stores a strict lease there. One active private project may use the network
at a time. A second exits 6. Releasing or reclaiming a lease requires an
identity audit; one user cannot take over another user's project.

## Platform dependencies

Native DEB and RPM packages carry hard dependencies for Quick mode:

| Format/arch | QEMU and firmware | Common packages |
|---|---|---|
| DEB amd64 | `qemu-system-x86`, `ovmf` | `qemu-utils`, `openssh-client`, `iproute2` |
| DEB arm64 | `qemu-system-arm`, `qemu-efi-aarch64` | `qemu-utils`, `openssh-client`, `iproute2` |
| RPM amd64 | `qemu-kvm`, `edk2-ovmf` | `qemu-img`, `openssh-clients`, `iproute` |
| RPM arm64 | `qemu-kvm`, `edk2-aarch64` | `qemu-img`, `openssh-clients`, `iproute` |

`farrow setup` fills the remaining private-mode capability:

- Debian/Ubuntu: the systemd package provides systemd-networkd;
  `qemu-system-common` supplies `qemu-bridge-helper` as part of the selected
  QEMU system package dependency chain.
- Fedora: setup installs `systemd-networkd`; `qemu-kvm` supplies the QEMU
  runtime and bridge helper.
- RHEL, Rocky, AlmaLinux, CentOS, and Oracle Linux: Quick is supported, but
  Private is not. Their normal host-network owner is NetworkManager and Farrow
  does not yet implement a safe NetworkManager backend.

## macOS private backend

Private mode uses a pinned upstream socket_vmnet daemon running from a
root-owned location; QEMU continues to run as the calling user.

`farrow setup meta` or `farrow setup full` performs the entire installation:

1. Fetch only the HTTPS URL embedded in the Farrow binary.
2. Limit redirects and archive size.
3. Verify the embedded SHA-256 and expected archive structure before publish.
4. Cache the verified archive in the user's Farrow cache with mode 0700/0600.
5. Generate one persistent interface UUID.
6. Install the root service and verify the configured host address exists.
7. Verify the packaged hosts helper against the digest embedded in the CLI and
   install that exact companion at `/opt/farrow/libexec/farrow-hosts-helper`.

The default vmnet mode is `host`; `farrow setup full --mode shared` explicitly
selects shared mode. A generated profile still moves to a free subnet when
possible. `VMNET_SHARING_SERVICE_BUSY (1009)` means another vmnet consumer is
holding the requested subnet; setup reports the conflict instead of retrying
forever or deleting the other service.

Install records the exact BSD interface it created in two protected identity
markers. Preflight accepts only that interface. A foreign VirtualBox, OrbStack,
Internet Sharing, or VPN interface holding `.1/24` is never adopted.

## Linux private backend

Private mode requires systemd-networkd, iproute tools, and a package-owned
`qemu-bridge-helper`.

Setup creates the root-owned `farrow0` bridge at `.1/24`, verifies that an
unprivileged QEMU process can attach through the bridge helper, and only then
persists the configuration. On Debian and Ubuntu, helper permission changes use
reversible `dpkg-statoverride`. On RPM hosts the package-owned helper state is
verified rather than rewritten.

If systemd-networkd is dormant, Farrow proves that no existing `.network`,
`.netdev`, or drop-in could claim a real host link before starting it. An
ambiguous Wi-Fi or Ethernet match is a hard stop because enabling networkd
could interrupt the connection used to administer the machine. On such a host
the clear choices are Quick mode, deliberate administrator migration to
systemd-networkd, or a supported host whose networkd ownership is already
established.

## Advanced inspection and manual transactions

Preflight is read-only and runs internally before setup installation and every
private lifecycle mutation:

```bash
farrow network preflight -f farrow.yaml --json
farrow network status --json
```

It detects mismatched installation state, overlapping routes/interfaces,
static addresses that already answer SSH, partial or unsafe ownership, an
unready backend, and macOS vmnet error 1009.

The manual install interface remains available when an administrator needs to
review or replay the exact lower-level transaction:

```bash
farrow network install                         # plan only
farrow network install --yes                   # apply on Linux

# macOS manual path; setup normally supplies these values itself
farrow network install \
  --archive /absolute/socket_vmnet-<pinned>-<arch>.tar.gz \
  --interface-id <persistent-uuid> \
  --cidr 10.10.10.0/24
farrow network install \
  --archive /absolute/socket_vmnet-<pinned>-<arch>.tar.gz \
  --interface-id <persistent-uuid> \
  --cidr 10.10.10.0/24 --yes
```

Manual `install` and `uninstall` always print their privileged plan and make no
change without `--yes`. They exist outside the beginner workflow precisely
because they expose host-global state.

## Changing or removing the private network

Changing one subnet component in isolation is invalid. First release the
lease, then replace the whole host/config decision:

```bash
farrow destroy --force
farrow network uninstall                       # review
farrow network uninstall --yes                 # apply
farrow setup full --network-cidr 172.31.251.0/24
farrow up
```

Uninstall refuses while a private lease is active, restores the recorded prior
host state, and preserves unrelated shared directories and the separately
installed hosts helper.
