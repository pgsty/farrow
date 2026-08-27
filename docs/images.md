# Guest images

Farrow uses a small, static-file image repository. The signed `catalog.json`
is the update channel; a new catalog or image does not require a new Farrow
binary. The binary retains one bootstrap catalog so that upstream fallback and
offline inspection still work before the public repository address is chosen.

## First pull

For `farrow image pull u24` or the first `farrow up`, Farrow:

1. resolves the Farrow home (`FARROW_HOME`, `storage.data_root`, then
   `~/.farrow` on both Linux and macOS);
2. fetches `catalog.json` and `catalog.json.minisig` from `--repo`,
   `FARROW_REPO`, or the release-build default repository, in that order;
3. verifies the catalog signature and selects the family's explicit `default`
   release for the native architecture;
4. reuses the readable local file only when its size, SHA-256, and qcow2
   structure match the catalog;
5. otherwise downloads `<repo>/<file>`, then tries the immutable upstream URL
   if that repository artifact is absent or invalid.

Progress is written to stderr. During a transfer it includes the selected URL,
catalog or HTTP content size, downloaded bytes, percentage, current average
rate, and ETA. The following checksum pass reports its own byte progress, so a
large local verification is distinguishable from a stalled download. URL
credentials, query strings, and fragments are not printed.

The default repository (`image.DefaultRepositoryURL`) is the development
host `https://m0/farrow` until the public image host goes live; the release
build will override it with Go `-ldflags -X`. A failed default sync falls
back to the embedded catalog, so machines that cannot reach it lose nothing.
Overrides:

```bash
farrow image pull --repo https://m0/farrow u24
# The same override is accepted by the lifecycle path:
farrow up --repo https://m0/farrow
# Or keep the override for the current shell:
export FARROW_REPO=https://m0/farrow
```

The signed catalog and digest-verified artifacts make plain HTTP acceptable
on a trusted LAN; a public repository must use HTTPS.

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
warning: image u24/amd64 (20260801.0.0) has status testing, not supported;
use only with the corresponding test/risk acceptance
```

The source user is bootstrap metadata only. Your login account is whatever
`ssh.user` resolves to, `dba` by default.

## Repository layout

The repository has only one image-family layer:

```text
catalog.json
catalog.json.minisig
SHA256SUMS                 # optional operator/human attachment
SHA256SUMS.minisig         # optional
socket_vmnet/              # macOS network backend mirror (see networking.md)
  socket_vmnet-1.2.2-arm64.tar.gz
  socket_vmnet-1.2.2-x86_64.tar.gz
u24/
  u24-20260801.0.0-amd64.qcow2
  u24-20260801.0.0-arm64.qcow2
d12/
  d12-20260806.2562.0-amd64.qcow2
  d12-20260806.2562.0-arm64.qcow2
```

`catalog.json` contains the aliases, explicit default release, architecture,
relative file, SHA-256, byte size, virtual size, boot mode, source user, status,
and immutable upstream URL. Aliases live inline with their image family; there
is no second short-name mapping file. The client needs only the catalog, its
signature, and the selected qcow2. `SHA256SUMS` is deliberately not part of the
client protocol—it exists so operators can run standard checksum tools over a
repository copy.

## Local layout and verification

```bash
farrow image pull u24
farrow image prune --dry-run
farrow image prune --yes
```

Downloaded filenames are not replaced by a digest. The native U24 image above
is stored as:

```text
~/.farrow/u24/u24-20260801.0.0-arm64.qcow2
```

There is no `cache/images/sha256` hierarchy and no per-image metadata sidecar.
Farrow's unrelated internal state remains in named directories such as
`projects/`, `manifests/`, and `locks/` under the same home.

A download is accepted only when all of this holds:

- the upstream URL is HTTPS and points at a versioned path — moving segments like
  `latest`, `current` and `release` are rejected in the catalog;
- the byte count matches the catalog, checked against both the advertised
  `Content-Length` and the completed stream;
- the SHA-256 matches the signed catalog;
- `qemu-img info` reports plain qcow2 with no backing file, no external data
  file, no encryption and no unknown incompatible features.

Verified artifacts become read-only. Runtime root disks are qcow2 overlays
created with an explicit `-F qcow2` backing format; the base is never modified.

`prune` lists unreferenced image files and crash-orphaned staging files, prints
their exact paths, and deletes nothing without `--yes`.
Images referenced by any project on the host are never candidates.

## Importing your own image

```bash
farrow image import --sha256 <digest> /path/to/image.qcow2

farrow image import --name local-mybase --boot uefi --source-user ubuntu \
  --sha256 <digest> /path/to/image.qcow2
```

The source basename is retained under `~/.farrow/local/`. With `--name` you
also register a local alias in the single `local-images.json` registry. Local
aliases must begin with `local-`; that namespace is forbidden in signed
catalogs, so a catalog update can never shadow an imported image. `--boot` plus
`--source-user` become required—Farrow will not guess firmware mode or bootstrap
account.

Imports go through the same qcow2 safety checks as downloads.

## Catalog updates and new images

The catalog is versioned and signed with minisign. The verifier embeds the
active and standby production public keys (`internal/image/keys.go`); the
private keys live on the repository build host under
`/data/repo/keys/farrow/`, and `tools/catalogsign` performs key generation,
signing, and verification with the exact implementation the CLI verifies
with:

```bash
CATALOGSIGN_PASSWORD=... go run ./tools/catalogsign sign \
  /data/repo/keys/farrow/farrow-catalog-active.key catalog.json SHA256SUMS
```

Rotation: sign with the standby key (already trusted by shipped binaries),
then land a release that embeds a fresh standby. A routine update is:

1. place the new qcow2 in its existing family directory using
   `<family>-<release>-<arch>.qcow2`;
2. calculate SHA-256, byte size, and qcow2 virtual size;
3. add the release under that family in `catalog.json`, changing `default` only
   after validation;
4. increment the monotonic catalog `version`, update `generated_at`, and sign
   the exact JSON bytes;
5. optionally regenerate and sign the combined `SHA256SUMS` attachment;
6. upload image bytes first, then atomically publish the signature and catalog.

Old versioned files remain addressable until no published catalog/project needs
them. A new family is the same process plus one new top-level directory and one
catalog object; aliases remain local to that object.

Manual catalog activation is also available:

```bash
farrow image sync https://repo.example/farrow/catalog.json
farrow image sync /absolute/repo/catalog.json
farrow image reset-manifest
```

`sync` reads the adjacent `.minisig`, verifies it against trusted keys, records
a version high-water mark, and refuses anything below it unless you pass
`--allow-downgrade`. Remote catalog URLs must not contain credentials, query
strings, or fragments; use an authenticated repository transport outside the
URL when private distribution is required. `reset-manifest` restores the
bootstrap catalog compiled into the binary.

Until active and standby production key custody is assigned, ordinary builds
contain no external catalog roots: the embedded catalog and `reset-manifest`
remain available, while `sync` fails closed. Tests inject development roots
explicitly; those roots are not compiled into release binaries.

Application release signatures and image catalog keys are separate trust
domains. Signing a release never authorizes a catalog, and vice versa.

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

Guests use the final Pigsty node-admin identity from first boot:
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
qcow2 before it becomes a catalog entry. It never downloads an artifact,
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
  --source-date-epoch 1787486400 --manifest-version 2026082601
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
