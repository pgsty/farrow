# Getting Started

Farrow turns a supported macOS or Linux host into a running VM lab with two
commands:

```bash
farrow setup
farrow up
```

`setup` installs the supported host dependencies, prepares the private
network, and writes the lab configuration (`pigsty.yml`) when the directory
has none. `up` downloads the guest image when needed and boots the VM(s).
Every node has a fixed, host-reachable IP address.

## Before you start

Farrow needs native virtualization acceleration; it does not fall back to
software emulation.

| Host | Status |
|---|---:|
| macOS arm64 (HVF) | supported |
| Debian/Ubuntu amd64 (KVM) | supported |
| Fedora amd64 (KVM) | supported |
| RHEL/Rocky/AlmaLinux/Oracle 9 (KVM) | NetworkManager backend implemented; native replay pending |
| macOS amd64, Linux arm64 | built and unit-tested |

On macOS, install Homebrew first — Farrow uses it to install QEMU but
deliberately does not run Homebrew's own remote installer for you. On Linux,
setup uses APT or DNF and asks for sudo when a package is missing. Preparing
the private network crosses the root boundary once, with the transaction
printed first.

Keep each lab in its own directory: the directory plus its configuration file
is the project.

## 1. Install Farrow

### From the current checkout (works today)

```bash
cd /path/to/farrow
make build
export PATH="$PWD/bin:$PATH"
farrow version
```

### From a release (after publication)

> The public `pgsty/farrow` repository and Releases page are not available
> yet; until then the checkout above is the working installation path.

```bash
curl -fsSL https://github.com/pgsty/farrow/releases/latest/download/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
```

Native DEB and RPM packages install QEMU, firmware, OpenSSH, and networking
dependencies automatically. Package installation does not change the host
network; the visible `farrow setup` transaction owns that decision.

## 2. Set up and start a lab

```bash
mkdir -p ~/farrow-labs/dev
cd ~/farrow-labs/dev
farrow setup            # single node; or: farrow setup dual|trio|full
farrow up
```

Setup prints one compact plan and asks once in a terminal. A healthy repeat
run is a no-op. When it succeeds, the last line is the next command:

```text
host:          darwin/arm64
profile:       meta
dependencies:  ready
network:       10.10.10.0/24 (installed)
config:        ~/farrow-labs/dev/pigsty.yml
status:        ready
next:          farrow up
```

If the directory already contains a `pigsty.yml` (for example a Pigsty
checkout after `./configure`), plain `farrow setup` prepares exactly that
file — the templates are only for empty directories.

## 3. Use the lab

```bash
farrow status
ssh dba@10.10.10.10          # nodes are ordinary IP-reachable machines
farrow ssh meta              # or via farrow, with pinned host keys
farrow exec node-1 -- hostname
farrow ss                    # install ~/.ssh aliases; then: ssh meta
farrow hosts install --yes   # optional: publish vm_alias names to /etc/hosts
```

Grow the lab by editing the file:

```bash
echo '        10.10.10.13: { nodename: node-3 }' # add under hosts:
farrow plan                  # shows: create node-3, nothing else
farrow up                    # creates node-3; running peers untouched
```

Shrinking is explicit: deleting a line only makes `plan` report the node as
`missing`; `farrow destroy node-3 --force` actually removes it.

## 4. Stop, start, remove

```bash
farrow stop                  # halt is an alias
farrow start
farrow reload                # stop + re-read configuration + converge
farrow destroy --force                    # keeps persistent disks, keys, marker
farrow destroy --force --delete-persistent
farrow destroy --force --purge            # terminal disposal: everything, including the registration
```

Deleted a lab directory without destroying it first? The registration is not
lost: `farrow list` flags it as an orphan, `farrow project prune` lists all
of them, and `farrow project rm <id> --force` destroys and deregisters it
from anywhere.

## Next steps

- [Configuration](config.md) — the inventory format and every `vm_*` knob
- [CLI reference](cli.md) — commands, flags, output contracts, exit codes
- [Networking](networking.md) — the private network and its backends
- [Pigsty integration](pigsty.md) — one file from `configure` to `install.yml`
- [Troubleshooting](troubleshooting.md) — symptom-to-resolution guidance
