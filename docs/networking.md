# Networking

Farrow has two network modes. Quick mode needs no privilege and no setup.
Private mode needs one host-global installation, performed once, explicitly.

## Quick mode (user NAT)

One VM behind QEMU's user-mode network stack. DHCP, DNS, the default route and
outbound internet all come from slirp. No bridge, no helper, no sudo.

Services reach the guest through loopback forwards:

| Host | Guest | Service |
|---|---|---|
| `127.0.0.1:2222` | `22` | SSH |
| `127.0.0.1:15432` | `5432` | Postgres |
| `127.0.0.1:13000` | `3000` | Grafana |
| `127.0.0.1:18080` | `80` | HTTP |
| `127.0.0.1:18443` | `443` | HTTPS |

If a preferred port is busy, Farrow tries `preferred + n × 10000` for
`n = 1..4` and stops. The port it settled on is recorded in the resolved spec
and printed by `status`. A remapped resolved forward also retains its original
request as `requested_host`, so an unchanged request can reuse the allocation
without hiding a later host-port edit. If every candidate is taken, free one or
pass an explicit `--forward`.

Add or replace forwards per run:

```bash
farrow up --forward 6432:6432 --forward 0.0.0.0:8080:80
farrow up --no-default-forwards --forward 5432:5432
```

Two limits worth knowing: user networking does not give the host a route to the
guest's own DHCP address or full ICMP behaviour, and a loopback forward is
reachable by every other local user on your machine. It blocks the LAN, not
your neighbours on the same host.

## Private mode (fixed IPs)

A private project gets one host-global RFC1918 IPv4 `/24` and every node gets a
static address on it.

| Address | Role |
|---|---|
| `.1` | host |
| `.2`–`.8` | DHCP range, and valid guest L2 VIPs at `.2`–`.4` |
| `.9`–`.254` | static node addresses |

Default is `10.10.10.0/24`. Only canonical RFC1918 `/24` ranges are accepted.

Every node has two NICs:

- **management** — user NAT: DHCP, DNS, default route, internet, and an SSH
  fallback on loopback;
- **private** — deterministic MAC, static address, no default route, no DNS, no
  IPv6 RA.

Private networking never silently degrades. If the private path fails, the
operation fails; it does not quietly fall back to NAT.

### One lab at a time

The network installer owns a root-owned runtime root — `/private/var/run/farrow`
on macOS, `/run/farrow` on Linux — holding a strict lease that serialises
access. A second private project exits **6**. Releasing or reclaiming a lease
requires an identity audit; one user cannot silently take over another's.

### Preflight

```bash
farrow network preflight -f farrow.yaml --json
```

Read-only, and run automatically before install and before every private
lifecycle mutation. It detects an installed network that does not match the
request, overlapping routes or interfaces, requested static addresses that
already answer SSH, partial or unsafe ownership, an unready backend, and macOS
vmnet error 1009.

If any address in `.9`–`.254` already accepts SSH while no Farrow lease is
active, `plan` and `up` fail before creating state and name the address. Stop
the other VM or pick a different subnet — do not bypass the probe.

## Choosing a subnet

If `10.10.10.0/24` collides with a VPN, LAN, or another hypervisor, choose one
replacement for the entire host *before* creating any project state:

```bash
farrow init full --network-cidr 172.31.251.0/24 >farrow.yaml
farrow network preflight -f farrow.yaml
farrow network install --cidr 172.31.251.0/24 --yes
farrow up -f farrow.yaml
```

Host `.1`, DHCP end `.8`, every node's last octet, the lease and the network
state all move together. A non-default subnet always warns, in both text and
JSON. Existing configurations are never silently remapped, and changing the
subnet later requires no active lease plus an explicit
`network uninstall` → `network install`.

## macOS install

Private mode uses a pinned upstream `socket_vmnet` daemon running as root from a
root-only path. QEMU itself stays unprivileged.

```bash
# 1. Review the plan — this changes nothing
farrow network install \
  --archive /absolute/socket_vmnet-1.2.2-arm64.tar.gz \
  --interface-id <persistent-uuid> \
  --cidr 10.10.10.0/24

# 2. Apply it
farrow network install \
  --archive /absolute/socket_vmnet-1.2.2-arm64.tar.gz \
  --interface-id <persistent-uuid> --yes
```

`--mode host` is the default. `--mode shared` works too, provided the chosen
subnet is genuinely free. Either way the install is only accepted once the
configured host address actually exists.

Install records which BSD interface it created: a `root:wheel` 0644 identity
marker under `/Library/Application Support/io.pgsty.farrow/` and a
byte-identical 0600 twin under `/private/var/db/farrow/`. Preflight accepts only
the interface named by that marker. A pre-existing foreign interface holding
`.1/24` — VirtualBox, OrbStack, macOS Internet Sharing — is reported as a
conflict and never adopted.

`VMNET_SHARING_SERVICE_BUSY (1009)` means another vmnet consumer holds the
subnet. Stop it, or move Farrow to a different `/24`.

To enable `/etc/hosts` integration from a tarball or Homebrew install, an
administrator must publish the helper explicitly:

```bash
sudo install -d -o root -g 0 -m 0755 /opt/farrow/libexec
sudo install -o root -g 0 -m 0755 \
  /path/to/farrow-hosts-helper /opt/farrow/libexec/farrow-hosts-helper
```

The CLI verifies the fixed path, every parent directory, owner, group, mode,
link count and the helper's digest before invoking it through sudo.

## Linux install

Private mode needs systemd, iproute2 and a package-owned `qemu-bridge-helper`
(Debian and Ubuntu ship it in `qemu-system-common`).

```bash
farrow network install          # dry plan
farrow network install --yes    # apply
```

The plan records the current state of systemd-networkd, NetworkManager,
`bridge.conf` and helper ownership.

**systemd-networkd must be safe to activate.** When networkd is dormant, Farrow
proves — before mutating anything — that no existing `.network` or `.netdev`
file could claim one of your real links. Starting networkd could otherwise
reconfigure the interface you are connected through. Ambiguous match patterns,
foreign netdevs and drop-ins are all hard conflicts, reported as exit 7:

```text
refuse to start inactive systemd-networkd:
  /usr/lib/systemd/network/80-wifi-adhoc.network could affect link wlp193s0
```

That particular file ships with systemd, so any host with a wireless interface
and dormant networkd will hit it. Farrow does not need the proof at all when
networkd is *already* active — adopting networkd is a deliberate host
administration decision, and Farrow will not make it for you. See
[troubleshooting.md](troubleshooting.md) for the options.

Apply creates the root-owned `farrow0` bridge at `.1/24`, verifies that an
unprivileged QEMU can attach through the bridge helper, and only then persists
the configuration. On Debian and Ubuntu, helper permission changes go through
reversible `dpkg-statoverride`; on RPM hosts the state is reported, not mutated.
Only Farrow's own marker blocks in bridge and NetworkManager configuration are
ever touched.

`farrow network uninstall --yes` restores the exact prior state and refuses
while a private lease is active.

## Removing the network

```bash
farrow destroy --force            # release the lease first
farrow network uninstall          # review
farrow network uninstall --yes
```

Uninstall preserves the separately installed hosts helper and any shared
`/opt/farrow` parent directories.
