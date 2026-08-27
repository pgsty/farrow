# Configuration

Quick mode needs no file. A private lab is declared in `farrow.yaml`, which
Farrow parses strictly: unknown keys, wrong types and trailing documents are all
errors. Generate one instead of writing it by hand:

```bash
farrow init full >farrow.yaml
farrow validate -f farrow.yaml
```

## Example

```yaml
version: 1
name: full
arch: native
network:
  mode: private
  cidr: 10.10.10.0/24
  host_address: 10.10.10.1
  dhcp_end: 10.10.10.8
defaults:
  image: u24
ssh:
  user: dba
nodes:
  - name: meta
    control: true
    host_aliases: [i.pigsty, api.pigsty, cli.pigsty]
    address: 10.10.10.10
    cpus: 2
    memory: 4GiB
    root_disk: 64GiB
    disks:
      - name: data
        size: 128GiB
        mount: /data
        filesystem: auto
        persistent: false
  - name: node-1
    address: 10.10.10.11
    cpus: 1
    memory: 2GiB
    root_disk: 64GiB
```

The machine-readable schema is `schemas/farrow-v1.schema.json`.

## Top level

| Key | Required | Type | Notes |
|---|---|---|---|
| `version` | yes | `1` | only value accepted |
| `name` | yes | DNS label | `[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?` |
| `arch` | no | `native` | only value accepted; no cross-architecture emulation |
| `network` | yes | object | see below |
| `defaults` | no | object | per-node fallbacks |
| `ssh` | no | object | login user and readiness timeout |
| `storage` | no | object | data root override |
| `nodes` | yes | array | 1–20 entries |

Sizes are `<integer><unit>` with unit `B`, `KiB`, `MiB`, `GiB`, `TiB`, `KB`,
`MB`, `GB` or `TB`. Durations are Go-style, e.g. `180s`, `5m`.

## network

| Key | Required | Notes |
|---|---|---|
| `mode` | yes | `user` (NAT, no privilege) or `private` (fixed IPs, host network required) |
| `cidr` | private | canonical RFC1918 IPv4 `/24` |
| `host_address` | private | must be the `.1` of `cidr` |
| `dhcp_end` | private | must be the `.8` of `cidr` |

Host `.1`, DHCP end `.8` and every node address move together. Static node
addresses live in `.9`–`.254`. See [networking.md](networking.md).

## defaults

| Key | Default | Notes |
|---|---|---|
| `image` | `u24` | any alias from `farrow image list` |
| `cpus` | `1` | 1–256; Quick CLI defaults to 2 |
| `memory` | `2GiB` | minimum `512MiB`; Quick CLI defaults to 4 GiB |
| `root_disk` | `64GiB` | grown on first boot; never shrunk |

Each node may override any of these.

## ssh

| Key | Default | Notes |
|---|---|---|
| `user` | `dba` | `[a-z_][a-z0-9_-]{0,31}` |
| `wait_timeout` | `180s` | how long `up` waits for guest SSH and the ready marker |

`dba` is created as UID 88 with primary group `admin` GID 88 — the Pigsty
node-admin identity, established at first boot so Ansible never has to change
the UID of its own live session. A custom `ssh.user` gets an ordinary account
without those fixed numeric IDs.

## storage

| Key | Notes |
|---|---|
| `data_root` | absolute, clean, non-root path for images, disks and state |

For a new project, resolution order is:

1. `FARROW_HOME`
2. `storage.data_root`
3. `~/.farrow` on both Linux and macOS

Broad roots—your home directory or the working directory—are rejected. The data
root itself must not be a symlink; existing ancestors are resolved even when
the final directory has not been created, so broad-root checks cannot be
bypassed through a symlinked parent.

For an existing project, the data root recorded in its project marker is
authoritative. Changing `FARROW_HOME` or `storage.data_root` does not move
state; a conflicting value fails with an explicit data-root migration error.

## nodes

| Key | Required | Notes |
|---|---|---|
| `name` | yes | DNS label, unique within the project |
| `control` | no | exactly one node may set this; it receives the lateral SSH key and publishes host aliases |
| `address` | private | static IPv4 in `.9`–`.254` of the project `/24` |
| `host_aliases` | no | names published by `hosts install` and the control node |
| `image` | no | overrides `defaults.image` |
| `cpus` | no | overrides `defaults.cpus` |
| `memory` | no | overrides `defaults.memory` |
| `root_disk` | no | overrides `defaults.root_disk` |
| `disks` | no | extra data disks |
| `shares` | no | opt-in host directories mounted through QEMU 9p |
| `forwards` | no | host→guest TCP forwards; user mode only |

`host_aliases` work inside the guest on every private subnet. Host-side
`farrow hosts install` currently publishes only default-subnet
`10.10.10.0/24` addresses.

### disks

| Key | Required | Notes |
|---|---|---|
| `name` | yes | `[a-z][a-z0-9-]{0,31}`, unique within the node |
| `size` | yes | sparse allocation |
| `mount` | yes | canonical absolute path; reserved system trees are rejected |
| `filesystem` | no | `auto` (default), `xfs` or `ext4` |
| `persistent` | no | `false` by default |

Disks get deterministic 20-character serials; the guest mounts them by ID and
filesystem UUID, not by device order.

`persistent: true` disks survive `destroy --force` and are reattached by a
later compatible `up`. Deleting them requires
`destroy --force --delete-persistent`.

### shares

| Key | Required | Notes |
|---|---|---|
| `host` | yes | existing, clean, absolute host directory owned by the invoking user |
| `guest` | yes | clean absolute guest mount path outside reserved system and SSH paths |
| `readonly` | no | `true` by default; set `false` explicitly for host↔guest writes |

Each node may declare at most eight shares. Sources must be real directories,
not symlinks, and cannot overlap Farrow's data, project, or marker paths.
Within a node, host sources and guest mount paths cannot overlap one another;
a guest share also cannot overlap a data-disk mount. The same host tree may be
shared by several nodes only when every overlapping export is read-only.

Shares are ordinary resolved intent: adding, removing, or changing one causes
Quick restart drift or Private recreate drift. Farrow checks that the selected
QEMU binary provides `virtio-9p-pci`, then cloud-init mounts the export with
`nodev,nosuid` and verifies its requested read-only/read-write behavior before
publishing the ready marker.

9p is a development convenience for source and small artifacts. It has weaker
performance and consistency than a guest filesystem, so do not put PostgreSQL
data, qcow2 images, or other write-heavy durable state on it. A guest with root
access is trusted with every directory you export.

### forwards

| Key | Required | Notes |
|---|---|---|
| `bind` | no | IPv4 only; defaults to `127.0.0.1` |
| `host` | yes | 1–65535 |
| `guest` | yes | 1–65535 |
| `protocol` | no | `tcp` only |

`host` is the requested (preferred) port. If that port is busy during a new
project's allocation, the versioned `resolved.json` keeps the selected port in
`host` and records the original request in an optional `requested_host` field.
`requested_host` is allocation evidence, not a YAML configuration key. It is
omitted when no remap occurred, and `farrow init quick` exports the original
request rather than turning a fallback into new intent.

Farrow reuses a selected fallback only while bind address, guest port,
protocol, and the original host-port request all still match. Changing `host`
therefore appears as Quick restart drift or Private recreate drift even when
the new value happens to equal the currently selected fallback.

Legacy resolved state has no `requested_host`. Farrow treats its persisted
`host` as the only safe compatibility baseline; it never guesses which busy
port may once have been requested. An old project that was remapped can
therefore report one explicit drift after upgrade. Either set the YAML/CLI
request to the persisted actual port if that is now the intended contract, or
review `plan` and apply the reported Quick `up --restart` / Private
`recreate --force` transition to establish unambiguous new state. Do not edit
`resolved.json` by hand.

## Built-in profiles

`farrow init <profile>` emits any of these. All use `dba`, ordinary nodes carry
one 128 GiB `/data` disk, and nodes whose name starts with `minio` carry four
32 GiB disks at `/data1`–`/data4`.

| Profile | Nodes | Purpose |
|---|---:|---|
| `meta` | 1 | single-node Pigsty |
| `dual` | 2 | primary + replica |
| `trio` | 3 | three-node HA |
| `full` | 4 | the standard demo lab |
| `minio` | 4 | four-node MinIO with 16 data disks |
| `citus` | 13 | Citus distributed cluster |
| `all` | 7 | mixed-distribution matrix |
| `oss` | 7 | open-source build matrix |
| `pro` | 7 | pro build matrix |
| `deb` | 5 | Debian-family build set |
| `rpm` | 2 | RPM-family build set |
| `deci` | 10 | ten-node scale test |
| `simu` | 20 | twenty-node simulation |

85 nodes across 13 profiles. `--scale 1..64` multiplies CPU and memory only;
`deci` and `simu` reject it. `all`, `deb`, `oss`, `pro` and `rpm` deliberately
mix guest distributions, so `--image` is refused unless you pass
`--force-uniform-image`.

## Environment variables

| Variable | Effect |
|---|---|
| `FARROW_HOME` | highest-precedence data root |
| `FARROW_REPO` | image repository URL or absolute local directory; overridden by `--repo` |
| `XDG_RUNTIME_DIR` | parent for QMP sockets and pidfiles; must be an owner-only 0700 absolute directory, otherwise a short UID-isolated fallback under `/tmp` is used |
