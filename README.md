# Farrow

Farrow runs Pigsty development VMs on macOS and Linux. It drives QEMU directly
from a single Go binary — no Vagrant, no Lima, no libvirt, no provider plugins.

Two modes, one tool:

- **Quick** — one VM, zero configuration, no privilege. `farrow up` and you have
  a machine.
- **Private** — a multi-node lab on fixed IPs, declared in `farrow.yaml`, with
  one explicitly installed host network.

## Requirements

| | macOS | Linux |
|---|---|---|
| Tier‑1 host | arm64, HVF | amd64, KVM |
| QEMU | 8.2.1+ (`brew install qemu`) | 6.2+ distro package |
| Also needed | `qemu-img`, OpenSSH | `qemu-img`, OpenSSH, iproute2 |
| Private mode extra | pinned socket_vmnet archive | systemd-networkd, `qemu-bridge-helper` |

macOS amd64 and Linux arm64 binaries are built and unit-tested, but not
regularly exercised on real hardware. Native acceleration is mandatory —
Farrow never falls back to TCG emulation.

## Install

```bash
sha256sum -c checksums.txt --ignore-missing     # shasum -a 256 -c on macOS
tar -xzf farrow_<version>_<os>_<arch>.tar.gz
install -m 0755 farrow_<version>_<os>_<arch>/bin/farrow ~/.local/bin/farrow
farrow doctor
```

RPM and DEB packages install `/usr/bin/farrow` plus the root-owned hosts helper.
Neither package installs or enables a private network — that is always an
explicit, reviewed step.

To build from source, see [docs/development.md](docs/development.md).

## Namespace transition

Farrow adopts a new namespace across the CLI, configuration, environment
variables, project state, host networking, installed files, and release
artifacts. This is not an in-place namespace alias. Before replacing a
pre-Farrow installation, use that release to stop or destroy its projects,
remove its SSH and hosts integrations, and uninstall its private host network;
back up any state or disks that must be retained. Do not operate both private
network identities on the same host at the same time.

## Quick VM in 60 seconds

No config file, no sudo:

```bash
farrow up                    # create and boot
farrow status                # address, ports, process identity
farrow exec -- uname -a      # run a command
farrow provision --script setup.sh --sudo  # explicit bounded Bash provisioning
farrow ssh                   # interactive shell
farrow stop
farrow start
farrow destroy --force
```

You get Ubuntu 24.04 with 2 CPUs, 4 GiB RAM, a 64 GiB root disk, a 64 GiB
`/data` disk, and login user `dba`. Postgres, Grafana, HTTP and HTTPS are
forwarded to `127.0.0.1:15432`, `:13000`, `:18080` and `:18443`.

Override anything inline:

```bash
farrow up --image el9 --cpus 4 --memory 8GiB --forward 6432:6432
```

Every command defaults to concise text and also supports machine-readable
output:

```bash
farrow status --json
farrow --yaml list
farrow up --verbose             # extra diagnostics stay on stderr
```

Terminal text uses restrained colour; redirected output and log files are
plain. Results stay on stdout while progress, warnings, errors and verbose
detail use stderr, so JSON/YAML can be piped directly into other tools. Long
operations report state without flooding the terminal. See the
[output conventions](docs/cli.md#conventions) for streaming logs and
interactive SSH.

For an opt-in host source mount, use `farrow.yaml`:

```yaml
nodes:
  - name: meta
    shares:
      - host: /absolute/path/to/pigsty
        guest: /workspace/pigsty
        readonly: false
```

Shares use QEMU 9p, default to read-only, and are intended for trusted
development guests and source trees. They are not a database or VM-disk
storage mechanism. See [config.md](docs/config.md#shares).

## Private lab in 5 minutes

The private network is a one-time, host-global installation. Review the plan
before applying it:

```bash
farrow init full >farrow.yaml       # 4 nodes on 10.10.10.10-.13
farrow validate -f farrow.yaml
farrow network preflight -f farrow.yaml

farrow network install              # prints the privileged plan, changes nothing
farrow network install --yes        # applies it

farrow up -f farrow.yaml
farrow provision --script bootstrap.sh --sudo --parallel 4
farrow ssh-config --install         # ssh meta, ssh node-1, ...
farrow hosts install --yes          # optional /etc/hosts entries
```

On macOS `network install` also needs `--archive <socket_vmnet tarball>` and
`--interface-id <uuid>`. If `10.10.10.0/24` collides with a VPN, LAN, or another
hypervisor, pick one replacement for the whole host *before* creating any
project:

```bash
farrow init full --network-cidr 172.31.251.0/24 >farrow.yaml
farrow network install --cidr 172.31.251.0/24 --yes
```

Tear down in the reverse order — `farrow destroy --force`, then
`farrow network uninstall --yes` once the lease is released.

## Documentation

| Document | Contents |
|---|---|
| [cli.md](docs/cli.md) | Every command, flag, and exit code |
| [config.md](docs/config.md) | `farrow.yaml` reference and the 13 built-in profiles |
| [networking.md](docs/networking.md) | Port forwards, subnet contract, host network install |
| [images.md](docs/images.md) | Guest images, cache, import and manifest sync |
| [pigsty.md](docs/pigsty.md) | The `pigsty-vm` integration and inventory rendering |
| [architecture.md](docs/architecture.md) | How it works internally |
| [security.md](docs/security.md) | Privilege boundary, deletion rules, secret handling |
| [troubleshooting.md](docs/troubleshooting.md) | Symptom → cause → fix |
| [development.md](docs/development.md) | Build, test, package, release |
| [status.md](docs/status.md) | What is verified and what blocks 1.0 |
| [phase-2.md](docs/phase-2.md) | Where the project goes after 1.0 |

## Design rules

These are enforced, not aspirational:

- QEMU runs as you. Quick mode needs no privilege at all; only host network
  installation and the `/etc/hosts` publisher touch root.
- Native architecture and native accelerator only. No silent TCG fallback.
- No arbitrary QEMU argument passthrough, and no global `nuke` command.
- Destroy, prune, repair and uninstall are bounded by ownership and exact
  paths. Anything ambiguous is preserved and reported, never guessed.
- One host-global private network and one active private project at a time.

## Status

Pre-1.0. The pre-rename functional baseline includes end-to-end Quick and
four-node `full` runs on both Tier‑1 hosts. A fresh native replay of the Farrow
namespace is still pending, alongside image hosting, release signing custody
and other verification gates. See [docs/status.md](docs/status.md).

## License

Apache-2.0. Third-party module versions and licenses are listed in
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
