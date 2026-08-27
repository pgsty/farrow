# Configuration

A Farrow lab is described by a Pigsty-compatible Ansible inventory. The same
document is Farrow's VM specification, Pigsty's deployment inventory, and the
bookkeeping record of the lab — one file, no rendering or reconciliation
between two formats.

```bash
farrow init            # write a single-node ./farrow.yml
farrow init full       # 4-node template
farrow validate        # parse, resolve, and print the spec hash
```

## Discovery and format

Commands that accept a configuration look for `-f <file>` first, then
`farrow.yml`, `farrow.yaml`, `pigsty.yml`, and `pigsty.yaml` in the working
directory. The name is a filename convenience only — the content format is
the same inventory in every case.

The retired `version:`/`nodes:` document format fails with migration
guidance; run `farrow init` for a fresh template.

## The namespace rule

Farrow reads exactly two things from the file and ignores everything else:

1. the **`vm_*` variables** below, and
2. a short whitelist of native Pigsty variables: the host key (its IP),
   `nodename`, `pg_cluster` + `pg_seq` (name derivation only), `admin_ip`,
   and `node_admin_username` / `node_admin_uid`.

Strictness is inverted at that boundary. Inside it, unknown `vm_*` keys,
wrong types, template expressions (`{{ … }}`), and conflicting group values
are hard errors. Outside it, the thousands of Pigsty parameters are opaque:
never read, never validated.

## Example

```yaml
all:
  vars:
    admin_ip: 10.10.10.10          # control node
    vm_image: u24                  # lab-wide default
  children:
    infra:
      hosts:
        10.10.10.10:
          nodename: meta
          vm_cpu: 4
          vm_mem: 8192
          vm_alias: [i.pigsty, a.pigsty]
    pg-test:
      hosts:
        10.10.10.11: { pg_seq: 1, pg_role: primary }
        10.10.10.12: { pg_seq: 2, pg_role: replica, vm_mem: "4GiB" }
      vars:
        pg_cluster: pg-test
        vm_disks: [{ path: /data, size: 64 }]
```

This defines three machines. `10.10.10.11` is named `pg-test-1` (from the
Pigsty `node_id_from_pg` convention), inherits the group's 64 GiB `/data`
disk, and takes every other value from the defaults.

## `vm_*` reference

Every variable has a default; a bare host entry (`10.10.10.13: {}`) is a
complete machine. Values resolve host vars over group vars over `all.vars`
over the built-in default.

| Variable | Default | Meaning |
|---|---|---|
| `vm_skip` | `false` | `true`: this host is not managed by Farrow (a real server in a mixed inventory) |
| `vm_image` | `u24` | guest image alias (`farrow image list`) |
| `vm_cpu` | `2` | CPU cores |
| `vm_mem` | `4096` | memory; integer = MiB, or a string with units (`"8GiB"`) |
| `vm_disk` | `64` | root disk; integer = GiB, grown on first boot, never shrunk |
| `vm_disks` | `[{path: /data}]` | extra data disks; see below |
| `vm_alias` | `[]` | names for `farrow hosts install` and guest `/etc/hosts` |
| `vm_shares` | `[]` | host directories exported over QEMU 9p; see below |

Whitelisted Pigsty variables Farrow honors:

| Variable | Default | Meaning |
|---|---|---|
| host key | — | the node's fixed private address |
| `nodename` | derived | VM name; falls back to `<pg_cluster>-<pg_seq>`, then `node-<last octet>` |
| `admin_ip` | first host | the control node (lateral SSH key, alias publication) |
| `node_admin_username` | `dba` | the guest login account; one user per deployment |
| `node_admin_uid` | `88` | must stay 88 for `dba` — Farrow creates the Pigsty node-admin identity at first boot |

Group-inheritance is deliberately simpler than Ansible's: deeper groups
override shallower ones, and two sibling groups supplying **different** values
for the same variable on the same host is an error — set it at host level.

### `vm_disks`

```yaml
vm_disks:
  - path: /data          # required; the mount point is the disk's identity
    size: 128            # GiB (or "128GiB"); default 128
    fs: xfs              # xfs (default) or ext4
    persistent: false    # true: survives destroy; reattached by a later up
```

An explicit `vm_disks: []` removes the default `/data` disk. Disks get
deterministic serials derived from the mount path; the guest mounts by disk
ID and filesystem UUID, so device enumeration order cannot move a mount.
`persistent: true` disks survive `destroy --force` and are deleted only by
`destroy --force --delete-persistent` (or `--purge`).

### `vm_shares`

```yaml
vm_shares:
  - host: /Users/me/src   # existing directory owned by the invoking user
    guest: /src           # clean absolute guest path
    readonly: true        # default; set false for host↔guest writes
```

At most eight shares per node; sources must be real directories; host sources
and guest paths must not overlap each other, data-disk mounts, Farrow's own
paths, or the SSH directory. 9p is a development convenience for source and
small artifacts — do not put PostgreSQL data on it. A guest with root access
is trusted with every directory you export.

## Derived network

The private `/24` is derived, not configured: every managed host must sit in
one canonical RFC1918 `/24`, `.1` is the host, `.2`–`.8` the DHCP boundary,
and node addresses live in `.9`–`.254`. Rebase a lab by editing the addresses
as one coordinated change (or `farrow init --network-cidr …` for a fresh
template).

## Drift and the node hash

`up` resolves the file into a per-node **node hash**: the deployment envelope
(network, login user, defaults) plus that node's own definition. Editing
anything outside the Farrow namespace — `pg_*`, `node_*`, `repo_*` — never
moves any hash and never causes drift.

- **Added host** → `up` creates it; running peers are untouched.
- **Changed node** → exit 4; `farrow plan` names it; apply with
  `farrow recreate --force <node>`.
- **Deployment-level change** (user, subnet, defaults) → whole-deployment
  recreate.
- **Removed host** → reported only; `farrow destroy <node> --force` removes it.

Two consequences worth knowing: a scale-out does not update the guest-side
`/etc/hosts` of existing peers (their seed predates the new node — Pigsty's
own `node_etc_hosts` management covers this, or recreate the peer), and the
in-place application of restart-class changes (`vm_cpu`, `vm_mem`) is not
implemented yet — today those are per-node recreates too.

## Data root and environment

| Variable | Effect |
|---|---|
| `FARROW_HOME` | data root override; default `~/.farrow` on both platforms |
| `FARROW_REPO` | image repository URL or absolute local directory; overridden by `--repo` |
| `FARROW_VMNET_ARCHIVE` | local socket_vmnet release tarball for macOS setup (digest-verified) |
| `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` | honored by every Farrow download |
| `FARROW_OUTPUT` / `FARROW_VERBOSE` | default presentation settings |
| `XDG_RUNTIME_DIR` | parent for QMP sockets and pidfiles |

Farrow writes nothing into the working directory beyond the configuration
file itself; images, disks, seeds, keys, and state all live under the data
root — `images/`, `nodes/<name>/`, `keys/`, `disks/`, and the applied
`state.json`. The resolved spec carries no data-root path, so pointing
`FARROW_HOME` elsewhere selects a different (initially empty) deployment
rather than causing drift; it does not move existing state.
