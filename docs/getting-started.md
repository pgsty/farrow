# Getting Started

Farrow turns a supported macOS or Linux host into a running VM lab with two
commands:

```bash
farrow setup
farrow up
```

`setup` installs the supported host dependencies, chooses and prepares the
network, creates the lab configuration when needed, and verifies the result.
`up` downloads the guest image when needed and starts the VM(s). You do not
need to run `doctor`, `validate`, `preflight`, or a manual network command
first.

This guide starts with the smallest lab and then shows the single-node and
multi-node private variants.

## Before you start

Farrow needs native virtualization acceleration. It does not silently fall
back to slow software emulation.

| Host | Quick lab | Private lab |
|---|---:|---:|
| macOS arm64 | yes, with HVF | yes |
| macOS amd64 | built and unit-tested | not regularly exercised on native hardware |
| Debian/Ubuntu Linux on amd64 | yes, with KVM | yes |
| Fedora Linux on amd64 | yes, with KVM | yes |
| RHEL/Rocky/AlmaLinux | yes, with KVM | not yet |
| Linux arm64 | built and unit-tested | not regularly exercised on native hardware |

On macOS, install Homebrew before running setup. Farrow uses Homebrew to
install QEMU, but deliberately does not run Homebrew's own remote installer
for you. On Linux, setup uses APT or DNF and may ask for `sudo` when a package
is missing.

Keep each lab in its own directory. Farrow uses the current directory and its
optional `farrow.yaml` to identify the project.

## 1. Install Farrow

### From the current checkout (works today)

On a fresh Mac that already has this checkout, run `brew install go` once.
Then build the development binary and keep it on `PATH` for this shell:

```bash
cd /path/to/farrow
make build
export PATH="$PWD/bin:$PATH"
farrow version
```

`make build` uses the current host OS and architecture. It requires the Go
toolchain; see [Development](development.md#local-development-build) for cross
builds and release packaging.

### From a release (after publication)

> **Current status:** the public `pgsty/farrow` repository and Releases page
> are not available yet. Until the first public release is published, the
> commands in this section are the intended release workflow, not a reachable
> download. Use the current-checkout path above today.

The release installer supports macOS and Linux on amd64 and arm64. It installs
Farrow for the current user in `~/.local/bin` and verifies the selected archive
against the release checksum manifest.

On a fresh Mac, the complete bootstrap will be:

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
if [[ -x /opt/homebrew/bin/brew ]]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
else
  eval "$(/usr/local/bin/brew shellenv)"
fi

curl -fsSL https://github.com/pgsty/farrow/releases/latest/download/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
farrow version
```

On Linux, use the same installer without the Homebrew lines:

```bash
curl -fsSL https://github.com/pgsty/farrow/releases/latest/download/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
farrow version
```

Native DEB and RPM packages are also published with formal releases. Installing
one through APT or DNF pulls in the architecture-specific QEMU, firmware,
OpenSSH, and networking packages automatically. Package installation does not
change the host network; the visible `farrow setup` transaction owns that
decision.

## 2. Choose a lab

Start with Quick unless you specifically need stable guest IP addresses or
multiple nodes.

| Lab | What it creates | Network | Setup command |
|---|---|---|---|
| Quick | one VM named `meta` | user NAT, no host network | `farrow setup` |
| Meta | one fixed-IP VM named `meta` | install or reuse private network | `farrow setup meta` |
| Full | four fixed-IP VMs | install or reuse private network | `farrow setup full` |

Quick uses QEMU user NAT and loopback port forwards. Meta and Full share one
Farrow-owned private network on the host; setup installs or safely reuses it.

## 3. Set up and start the lab

### Quick: the recommended first run

```bash
mkdir -p ~/farrow-labs/quick
cd ~/farrow-labs/quick
farrow setup
farrow up
```

Quick makes no host-network change. If QEMU or another host dependency is
missing, setup shows one compact plan and installs only what is needed. When
setup succeeds, its final line tells you to run `farrow up`.

A successful setup ends with a compact summary like this:

```text
host:          darwin/arm64
mode:          quick
profile:       quick
dependencies: ready
network:       user NAT
status:        ready
next:          farrow up
```

### Meta: one fixed-IP node

```bash
mkdir -p ~/farrow-labs/meta
cd ~/farrow-labs/meta
farrow setup meta
farrow up
```

Setup creates `farrow.yaml`, prepares the private network, and leaves the
directory ready for plain `farrow up`. There is no separate network or config
step.

### Full: four fixed-IP nodes

```bash
mkdir -p ~/farrow-labs/full
cd ~/farrow-labs/full
farrow setup full
farrow up
```

This is the complete multi-node bootstrap. The same `up` command starts all
four nodes. Image downloads, package installation, and VM readiness checks
emit restrained progress so a long first run does not look stuck.

If setup needs to change the host, it prints the transaction and asks once in
an interactive terminal. Review it and press Enter to continue. A healthy
repeat run is a no-op.

## 4. Confirm the lab is ready

Run these commands from the lab directory:

```bash
farrow status
farrow exec -- uname -a
farrow ssh
```

`status` shows the nodes and their endpoints. `exec` runs one non-interactive
command in the guest. `ssh` opens an interactive shell; with one node, no node
name is required.

For the Full profile, select a node when needed:

```bash
farrow ssh meta
farrow exec node-1 -- hostname
```

Optionally install namespaced OpenSSH aliases after the lab is running:

```bash
farrow ss
ssh "$(basename "$PWD")-meta"
```

For a lab directory named `dev`, `farrow ss` is equivalent to
`farrow ssh-config --install --name dev`, and the canonical alias is
`dev-meta`. Pass `--name` when you want a different stable prefix.

At this point the lab is usable. The remaining sections explain decisions and
automation; they are not additional setup steps.

## What setup handles for you

One `setup` transaction owns the entire first-run decision:

- detect the host OS, architecture, package manager, and native accelerator;
- install only missing QEMU, image, firmware, SSH, and networking capabilities;
- use QEMU user NAT for Quick, with no privileged network service;
- for Meta or Full, safely choose or reuse one RFC1918 `/24` and prepare the
  platform network;
- verify the paired privileged helper for private labs;
- create `farrow.yaml` atomically for a named profile; and
- verify the final state and print the next command.

Farrow automatically resolves choices that are safe and reversible. It stops
instead of deleting, adopting, or overwriting state whose ownership is not
clear.

| Existing state | What setup does |
|---|---|
| matching healthy Farrow network | reuse it |
| healthy Farrow network on another subnet | align a newly generated profile with it |
| default subnet is occupied, but the profile is new | choose a safe built-in alternative |
| matching generated `farrow.yaml` | reuse it |
| different valid `farrow.yaml` | preserve and prepare that file with plain `farrow setup`, or use an empty directory |
| invalid `farrow.yaml` | preserve it; fix it or use an empty directory |
| foreign interface, partial install, or unsafe ownership | preserve it and report the exact boundary |
| another project holds the private-network lease | preserve it and identify the project to stop |
| macOS has no Homebrew | ask you to install Homebrew, then rerun the same setup command |
| private mode on a NetworkManager-owned RHEL-family host | keep the host unchanged; use Quick or a supported private host |

For the complete network decision table, see
[Networking](networking.md#what-setup-does). For a blocked run, start with the
resolution printed by setup; [Troubleshooting](troubleshooting.md) covers the
common cases.

## Automation and output formats

Text is the default output. A terminal gets restrained colour; redirected text
is plain. Use `--json` or `--yaml` for machine-readable stdout, and
`--verbose` for bounded diagnostics on stderr.

Inspect a setup transaction without changing the host:

```bash
farrow setup full --dry-run --json
```

Apply the same transaction without an interactive prompt:

```bash
farrow setup full --yes --json
farrow up --json
```

In a dry-run result, `applicable: true` means the plan is safe to apply;
`ready` remains false because nothing was changed. `next_argv` contains the
same setup command without `--dry-run`, which automation can invoke without
parsing shell text.

Progress, warnings, and verbose details go to stderr, so JSON or YAML on stdout
remains parseable. `--json`, `--yaml`, and `--verbose` are the only global
presentation flags; JSON and YAML are mutually exclusive.

## Daily use

```bash
farrow status                 # inspect this lab
farrow ssh                    # open the only/default node
farrow ss                     # install namespaced OpenSSH aliases
farrow stop                   # stop the VM(s)
farrow start                  # start an existing stopped lab
farrow up                     # create or converge the lab
```

Removing a lab is deliberately explicit:

```bash
farrow destroy --force
```

That removes the current project but preserves disks marked `persistent: true`.
Deleting those disks additionally requires `--delete-persistent`. A private
host network is reusable and does not need to be uninstalled after each lab.

## Next steps

- [CLI reference](cli.md) — every command, flag, output contract, and exit code
- [Configuration](config.md) — `farrow.yaml` and the built-in profiles
- [Networking](networking.md) — Quick forwards and private-network decisions
- [Troubleshooting](troubleshooting.md) — symptom-to-resolution guidance
- [Development](development.md) — local builds, cross builds, packages, and releases
