# Installation

Piglet v1 targets native macOS arm64/Linux amd64 hosts. Tier-2 binaries are
built for macOS amd64 and Linux arm64, but their real-host smoke is periodic.
The host needs a supported native QEMU, `qemu-img`, OpenSSH and enough disk for
the immutable image cache plus sparse project overlays.

## Verify a release

Do not install a binary before checking all published layers:

```bash
shasum -a 256 -c checksums.txt
jq -e '.spdxVersion | startswith("SPDX-")' piglet_*.spdx.json
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/pgsty/piglet/.github/workflows/release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
cosign verify-blob-attestation checksums.txt \
  --bundle checksums.provenance.sigstore.json \
  --certificate-identity 'https://github.com/pgsty/piglet/.github/workflows/release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --type slsaprovenance1
```

The exact identity/tag is part of release policy; do not use an unrestricted
regular expression. Development artifacts are explicitly unsigned and must
not be presented as GA.

## Tarball or Homebrew

Extract the matching native archive and install the ordinary CLI without root:

```bash
tar -xzf piglet_1.0.0_linux_amd64.tar.gz
cd piglet_1.0.0_linux_amd64
install -m 0755 bin/piglet "$HOME/.local/bin/piglet"
piglet doctor
```

The Homebrew formula installs QEMU plus the unprivileged CLI. Quick mode needs
no sudo. The archive also contains the narrow `piglet-hosts-helper`; Homebrew
keeps its Cellar copy unprivileged and never runs it as root.

## RPM/DEB

The RPM and DEB packages install:

```text
/usr/bin/piglet                                  root:root 0755
/opt/piglet/libexec/piglet-hosts-helper         root:root 0755
/usr/share/piglet/schemas/piglet-v1.schema.json root:root 0644
```

They do not silently install or enable a private network. Run
`piglet network preflight` (or `-f piglet.yaml`) first, review
`piglet network install`, then explicitly apply with `--yes`. Install runs the
same preflight again immediately before mutation.
Package metadata recommends (rather than hard-depends on) the native QEMU,
`qemu-img`, OpenSSH, and iproute packages because names/capabilities differ by
distribution; `doctor` remains the final capability check.

## macOS private network

Quick mode does not need socket_vmnet. Private mode uses the pinned upstream
archive and a stable interface UUID:

```bash
piglet network install \
  --archive /absolute/socket_vmnet-1.2.2-arm64.tar.gz \
  --interface-id <persistent-uuid> \
  --cidr 10.10.10.0/24

# Review paths, hashes, owner/mode and rollback, then:
piglet network install \
  --archive /absolute/socket_vmnet-1.2.2-arm64.tar.gz \
  --interface-id <persistent-uuid> --yes
```

Host is the default; `--mode shared` is also supported when the selected global
subnet is free. Both modes must create the configured host address before the
install is accepted. `VMNET_SHARING_SERVICE_BUSY (1009)` means another vmnet
consumer conflicts with the subnet. QEMU stays unprivileged.

Fresh install also proves which BSD interface it created. It writes a
root:wheel 0644 non-secret identity marker below
`/Library/Application Support/io.pgsty.piglet/` and a byte-identical 0600 twin
below `/private/var/db/piglet/`. UUID/CIDR/host/BSD-name mismatch, a
pre-existing foreign `.1/24` interface, or a listening socket without that
proof fails closed; Piglet never adopts a VirtualBox/OrbStack/system interface
by address.

For an explicit escape subnet, first generate a matching profile and preflight
it, then pass the same CIDR to install:

```bash
piglet init full --network-cidr 172.31.251.0/24 >piglet.yaml
piglet network preflight -f piglet.yaml
piglet network install --mode shared --cidr 172.31.251.0/24 \
  --archive /absolute/socket_vmnet-1.2.2-arm64.tar.gz \
  --interface-id <new-persistent-uuid> --yes
```

Changing CIDR or mode requires no active lease and an explicit
uninstall→install. Network uninstall preserves the separately installed hosts
helper and shared `/opt/piglet` parents.

To enable optional `/etc/hosts` integration from a tarball/Homebrew install,
an administrator must explicitly publish the companion helper:

```bash
sudo install -d -o root -g 0 -m 0755 /opt/piglet/libexec
sudo install -o root -g 0 -m 0755 \
  /path/to/piglet-hosts-helper \
  /opt/piglet/libexec/piglet-hosts-helper
```

The CLI checks the fixed path, every parent, owner/group/mode/link count and
the companion digest before sudo executes it.

## Linux private network

Private mode requires systemd, iproute2 and a package-owned
`qemu-bridge-helper`. The dry plan records systemd-networkd, NetworkManager,
bridge.conf and helper ownership state. When networkd is inactive, the dry plan
also contains a pre-mutation proof that existing networkd configuration cannot
claim a host link; ambiguous matches, non-Piglet netdevs, and drop-ins are hard
conflicts. Explicit apply creates `piglet0` and verifies a non-root QEMU attach
before persisting. `network uninstall --yes` restores the exact prestate and
refuses while a private lease is active.

Linux accepts the same `--cidr RFC1918/24` contract. Its manifest and
systemd-networkd unit use host `.1`; profile/config node suffixes and DHCP `.8`
must match exactly. A non-default CIDR is always present in JSON warnings and
stderr text output.

Never copy a development installation onto a production server without a new
plan and owner review.
