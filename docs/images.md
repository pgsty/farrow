# Guest images

Farrow ships an embedded manifest of guest images, each pinned to an exact
SHA-256. Images are downloaded on demand into a content-addressed cache shared
by every project on the host.

## Available aliases

```bash
farrow image list
farrow image info u24
```

| Alias | Distribution | Release | Virtual size | Source user |
|---|---|---|---:|---|
| `el9` | Rocky Linux 9 | 9.8 | 10 GiB | `rocky` |
| `el10` | Rocky Linux 10 | 10.2 | 10 GiB | `rocky` |
| `d12` | Debian 12 bookworm | 20260806 | 3 GiB | `debian` |
| `d13` | Debian 13 trixie | 20260810 | 3 GiB | `debian` |
| `u22` | Ubuntu 22.04 jammy | 20260810 | 2.2 GiB | `ubuntu` |
| `u24` | Ubuntu 24.04 noble | 20260801 | 3.5 GiB | `ubuntu` |
| `u26` | Ubuntu 26.04 resolute | 20260731 | 3.5 GiB | `ubuntu` |

Each alias has one `amd64` and one `arm64` artifact — 14 entries in total, all
UEFI. Farrow only ever resolves the artifact matching your native architecture.

All entries are currently marked `testing`. Nothing is published as `supported`
until self-hosted artifacts and their key custody are in place, so every start
prints a warning naming the image and its status:

```text
WARNING: image u24/amd64 (20260801.0.0) has status testing, not supported;
use only with the corresponding test/risk acceptance
```

The source user is bootstrap metadata only. Your login account is whatever
`ssh.user` resolves to, `dba` by default.

## Cache and verification

```bash
farrow image pull u24
farrow image prune --dry-run
farrow image prune --yes
```

A download is accepted only when all of this holds:

- the URL is HTTPS and points at a versioned path — moving segments like
  `latest`, `current` and `release` are rejected in the catalog;
- the byte count matches the manifest, checked against both the advertised
  `Content-Length` and the completed stream;
- the SHA-256 matches the manifest;
- `qemu-img info` reports plain qcow2 with no backing file, no external data
  file, no encryption and no unknown incompatible features.

Verified artifacts become read-only. Runtime root disks are qcow2 overlays
created with an explicit `-F qcow2` backing format; the base is never modified.

`prune` lists unreferenced cache pairs and deletes nothing without `--yes`.
Images referenced by any project on the host are never candidates.

## Importing your own image

```bash
farrow image import --sha256 <digest> /path/to/image.qcow2

farrow image import --name mybase --boot uefi --source-user ubuntu \
  --sha256 <digest> /path/to/image.qcow2
```

Without `--name` the file enters the cache addressed by digest. With `--name`
you also register a local alias, and `--boot` plus `--source-user` become
required — Farrow will not guess firmware mode or bootstrap account.

Imports go through the same qcow2 safety checks as downloads.

## Manifest updates

The manifest is versioned and signed with two minisign keys. Updates are
explicit:

```bash
farrow image sync https://example.com/farrow-manifest.json
farrow image sync ./manifest.json
farrow image reset-manifest
```

`sync` verifies the detached signature against both keys, records a version
high-water mark, and refuses anything below it unless you pass
`--allow-downgrade`. `reset-manifest` restores the manifest compiled into the
binary.

Application release signatures and image manifest keys are separate trust
domains. Signing a release never authorizes a manifest, and vice versa.

## What an image must provide

Any image you import has to satisfy the same contract as the built-in ones:

- cloud-init with the NoCloud datasource, and an OpenSSH server;
- a writable serial console, virtio block and virtio network support;
- root partition growpart plus filesystem resize on first boot;
- a management NIC that takes DHCP by MAC, and a private NIC that accepts a
  static address with no default route and no DNS.

Farrow builds the guest seed itself: a pure-Go ISO9660 NoCloud CIDATA image,
mounted read-only. The seed sets up the login account, mounts data disks by
disk ID and filesystem UUID, and writes a generation-matched ready marker only
after disk identity, mount and — for private nodes — exact `private0`
address/route/DNS checks all pass.

## The `dba` identity

Built-in profiles use the final Pigsty node-admin identity from first boot:
`dba` as UID 88 with primary group `admin` GID 88, a real `/home/dba`, bash, a
locked password and key-only passwordless sudo.

Group creation fails closed if GID 88 is already taken by an unrelated group,
and readiness fails on any passwd, group or home-ownership mismatch. Getting
this right at boot is what lets Pigsty's node-admin task stay idempotent
instead of changing the UID of its own live SSH session.

A custom `ssh.user` gets an ordinary account and does not inherit these numeric
IDs.

## Preparing a normalized image

`packaging/image-pipeline/` validates and optionally normalizes a candidate
qcow2 before it becomes a manifest entry. It never downloads an artifact,
uploads anything, touches a runtime file, or marks an image `supported`.

```bash
packaging/image-pipeline/build.sh \
  --mode validate \
  --source /absolute/noble-server-cloudimg-arm64.img \
  --expected-sha256 <digest> \
  --output /absolute/candidate-u24-arm64 \
  --name u24 --release 20260801.0.0 --arch arm64 \
  --source-user ubuntu --boot uefi \
  --artifact-url https://example.com/u24/{sha256}.qcow2 \
  --license "Ubuntu cloud image" \
  --source-date-epoch 1787486400 --manifest-version 2026082402
```

`--mode validate` needs only Python 3 and `qemu-img`: it copies and re-hashes
the source, forces qcow2 parsing, checks the backing chain, runs
`qemu-img check`, and emits a reproducible evidence bundle. It changes no guest
credentials, and says so in both stdout and `validation.json`.

`--mode offline` additionally requires libguestfs `virt-customize` and
`virt-cat`. It mutates only the staged copy, with `--no-network`, inside the
offline appliance: it establishes `admin` GID 88 and `dba` UID 88, locks every
password hash, disables SSH password and root login, and strips old
`authorized_keys`, known development keys, shell histories, SSH host keys,
machine-id, random seed and cloud-init cache — then verifies those
postconditions before writing a deterministic marker the host reads back.
