# Farrow

Farrow turns one Pigsty-compatible Ansible inventory into one fixed-IP local
QEMU deployment on macOS or Linux. The inventory you hand to Pigsty is the same
file that describes the virtual machines, so there is no second project format
to keep in sync.

Authoritative documentation: <https://farrow.pgsty.com/>

```bash
farrow init          # write ./farrow.yml
farrow setup         # prepare the host: QEMU, firmware, fixed-IP network
farrow up            # create, boot, and wire SSH for every node
farrow ssh meta      # you are in
```

## What it is

- **One inventory, one deployment.** `vm_*` host variables describe the guests;
  everything else in the file stays opaque and is passed through to Pigsty
  untouched. There is no separate VM manifest.
- **Fixed IPs, not DHCP.** Nodes get the addresses the inventory names, on a
  host-global private network Farrow installs once. `10.10.10.10` is
  `10.10.10.10` across reboots and recreates.
- **Declarative, but never surprising.** `farrow plan` shows the difference
  between the inventory and the applied state. Removing a host from the file
  never destroys a machine — deletion is always an explicit `farrow destroy`.
- **Verified images.** Guest images come from a signed catalog with SHA-256,
  qcow2, and virtual-size verification on every fetch, and immutable upstream
  URLs as the fallback source.
- **Fail-closed lifecycle.** QMP identity plus full process identity, atomic
  state writes, and transaction journals mean an interrupted operation is
  recoverable rather than ambiguous.

Farrow is a local development-lab runtime. It is not a cluster manager, not a
cloud provisioner, and not a container runtime.

## Requirements

| Host | Accelerator | Minimum QEMU | Tier |
|---|---|---|---|
| macOS arm64 | HVF | 8.2.1 | 1 |
| macOS amd64 | HVF | 8.2.1 | 2 |
| Linux amd64 | KVM | 6.2 | 1 |
| Linux arm64 | KVM | 6.2 | 2 |

Tier 1 is the dated, natively validated matrix; tier 2 is cross-built and
package-verified against the narrower status published at
<https://farrow.pgsty.com/docs/about/status/>. You also need `qemu-img`, UEFI
firmware for arm64 guests, and an OpenSSH client.

`farrow doctor` reports exactly what is present, what is missing, and how to fix
it. `farrow setup` installs what it can and asks for administrator access only
when a host transaction genuinely needs it.

## Install

Farrow is pre-1.0. A successful build from source is not evidence of a tagged
release, a published package, or a supported guest image.

```bash
# From a release: user-scoped, no sudo, checksum-verified
FARROW_VERSION=<version> ./install.sh

# Homebrew formula (shipped as a release asset)
brew install --formula ./farrow.rb

# Debian/Ubuntu and RHEL-family packages are release assets too
sudo apt install ./farrow_<version>_linux_amd64.deb
sudo dnf install ./farrow_<version>_linux_amd64.rpm
```

From source:

```bash
make build
export PATH="$PWD/bin:$PATH"
```

## Everyday commands

```bash
farrow init full             # a four-node inventory instead of one
farrow validate              # parse and resolve without touching anything
farrow plan                  # what would change, and why
farrow up                    # converge
farrow status                # applied state, from any directory
farrow ssh meta -- uptime    # run something in a guest
farrow ss                    # teach plain `ssh` about the nodes
farrow hosts install         # publish node names into the host hosts file
farrow destroy               # explicit, confirmed teardown
```

Every command accepts `--json` or `--yaml` for stable machine-readable output.
Presentation flags never change an exit status.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | runtime failure |
| 2 | usage error |
| 3 | missing host capability |
| 4 | state conflict (no deployment, wrong phase) |
| 5 | partial success across selected nodes |
| 6 | resource conflict (address or port in use) |
| 7 | integrity failure (digest, signature, or state mismatch) |

## State

Everything Farrow owns lives under `$FARROW_HOME` (default `~/.farrow`): the
applied deployment, per-node state and journals, the verified image cache, and
the signed catalog. Applied-state commands therefore work from any directory,
and removing that one tree removes Farrow's footprint apart from the host
network and the hosts-file entries, which have their own `uninstall` and
`remove` commands.

## Development

```bash
make build     # build into ./bin
make test      # unit tests
make check     # the complete source gate CI runs
```

`make check` covers module integrity, shell syntax, unit and race tests, `vet`,
Staticcheck, `govulncheck`, cross-compilation for all four targets, the image
pipeline boundaries, and the dependency-license inventory. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## Security

Farrow asks for root only for two narrowly scoped host transactions: installing
the private network, and publishing node names into the system hosts file
through a separate, digest-pinned helper binary. See
[SECURITY.md](SECURITY.md) for the privilege boundary and how to report a
vulnerability.

## License

Apache-2.0. Release tooling reconstructs dependency license texts from the
exact module versions pinned by `go.mod`, and ships them inside every archive
and package.
