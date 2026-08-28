# Farrow

Farrow turns a Pigsty inventory into running QEMU virtual machines. It is the
sandbox layer under [Pigsty](https://pigsty.io): one Go binary driving QEMU
directly — no Vagrant, Lima, libvirt, provider plug-in, or hand-written host
bootstrap.

One file describes the lab. It is a Pigsty-compatible Ansible inventory —
`farrow.yml`, or the `pigsty.yml` Pigsty itself writes — so the same document
is the VM specification for Farrow and the deployment inventory for Pigsty:

```yaml
all:
  vars:
    admin_ip: 10.10.10.10
  children:
    nodes:
      hosts:
        10.10.10.10: { nodename: meta }
        10.10.10.11: { nodename: node-1, vm_mem: 8192 }
```

Farrow reads only the `vm_*` variables (plus `nodename` and `admin_ip`);
everything else belongs to you or to Pigsty. Every parameter has a default, so
a bare `10.10.10.12: {}` line is a complete machine: fixed IP, 2 cores, 4 GiB
memory, 64 GiB root disk, one 128 GiB `/data` disk, Ubuntu 24.04.

## Start here

From the current checkout, build Farrow and start a lab:

```bash
cd /path/to/farrow
make build
export PATH="$PWD/bin:$PATH"

mkdir -p ~/lab && cd ~/lab
farrow setup          # prepare host + write farrow.yml (single node)
farrow up             # boot it: ssh dba@10.10.10.10
```

`farrow setup meta|dual|trio|full` selects a 1/2/3/4-node template; with an
existing configuration in the directory (`farrow.yml` first, else
`pigsty.yml`), plain `farrow setup` prepares exactly that file. Every node
gets a fixed, host-reachable IP — you can `ping` it, `ssh dba@10.10.10.10`
into it, and point Ansible at it.

Farrow manages **exactly one deployment per user**: all state lives under
`~/.farrow` (images in `images/`, per-node artifacts in `nodes/`, one SSH
key pair in `keys/`), nothing is written into your working directory, and
`status`/`stop`/`ssh` work from any directory. `up` and `plan` read the
configuration in front of you and diff it against the deployment,
node by node.

## Day-to-day commands

```bash
farrow status
farrow ssh meta
farrow exec node-1 -- hostname
farrow ss             # one-time: install ~/.ssh aliases (ssh meta works)
farrow stop           # halt is an alias
farrow start
farrow reload         # stop + re-read configuration + converge
farrow plan           # classify pending changes; nothing mutates
farrow destroy --force
```

Editing the inventory is the normal workflow: add a host line and `farrow up`
creates just that node; resize a node and `plan` reports its per-node
`recreate`; delete a line and Farrow only reports it — removal is always the
explicit `farrow destroy <node> --force`. Configuration absence never implies
destruction. `farrow init meta|dual|trio|full` writes a fresh `farrow.yml`
to edit or to copy into a Pigsty checkout as `pigsty.yml`.

Text is the default output. Use `--json` or `--yaml` for automation and
`--verbose` for bounded diagnostics on stderr.

## Pigsty in one flow

```bash
./configure -c meta        # Pigsty writes pigsty.yml (vm_* knobs included)
farrow up                  # Farrow boots the same file
./install.yml              # Ansible deploys against the same file
```

Guests are born as Pigsty expects them: the `dba` admin account with UID 88
exists before Ansible ever connects, and `node_admin_username` in the
inventory is honored.

## Supported hosts

| Host | Status |
|---|---|
| macOS arm64 (HVF) | supported; pinned socket_vmnet |
| Linux amd64 (KVM), Debian/Ubuntu | supported; systemd-networkd or NetworkManager backend |
| Linux amd64 (KVM), Fedora | supported |
| RHEL / Rocky / AlmaLinux / Oracle 9 | NetworkManager backend implemented; native replay pending |
| macOS amd64, Linux arm64 | built and unit-tested; not regularly exercised natively |

Native acceleration remains the normal path. Farrow selects TCG only when the
user explicitly chooses a foreign `vm_arch`, or for a catalogued incompatibility
such as the stock EL8 arm64 64K kernel on Apple Silicon. The selected runtime is
visible in status output; arbitrary native failures never trigger a retry.

## Documentation

| Document | Contents |
|---|---|
| [getting-started.md](docs/getting-started.md) | installation and the two-command start |
| [cli.md](docs/cli.md) | command, flag, output, and exit-code reference |
| [config.md](docs/config.md) | the inventory format and the `vm_*` reference |
| [networking.md](docs/networking.md) | the private network, backends, and traffic paths |
| [images.md](docs/images.md) | image repository, local files, import, signed catalog |
| [pigsty.md](docs/pigsty.md) | the one-file Pigsty integration |
| [architecture.md](docs/architecture.md) | internal components and trust boundaries |
| [security.md](docs/security.md) | privilege, deletion, and secret handling |
| [troubleshooting.md](docs/troubleshooting.md) | symptom to cause to fix |
| [development.md](docs/development.md) | local, cross, package, and release builds |
| [status.md](docs/status.md) | verified evidence and pre-1.0 gates |
| [phase-2.md](docs/phase-2.md) | post-1.0 direction |

## Safety model

- QEMU runs as your user. `setup` may use the system package manager through
  sudo to install missing dependencies; after that, only host-network
  installation and the optional `/etc/hosts` publisher cross the root boundary.
- Native architecture and acceleration by default; bounded, visible TCG only
  for explicit architecture choice or a known image/host incompatibility.
- Destructive operations are ownership- and path-bounded. Ambiguous state is
  preserved and reported. Removing a node from the configuration never
  destroys it.
- One host-global private network serving the one deployment. There are no
  projects, registries, or leases to manage.

The project is pre-1.0. See [status.md](docs/status.md) for the current
evidence and external release gates; [REDESIGN.md](REDESIGN.md) records the
product redirection this tree implements.

Farrow is licensed under Apache-2.0. Third-party module versions and licenses
are listed in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
