# Image and guest contract

A formal image entry is native-architecture qcow2 with no external backing or
data file. It declares its firmware mode, immutable digest, exact artifact byte
size, virtual size, source bootstrap user, provenance, build recipe, license
facts, and per-arch smoke status. Being present in the formal alias matrix does
not by itself make an artifact supported.

The runtime login user is the resolved `ssh.user`; the active execution prompt
sets the default to `dba`. The source image user is bootstrap metadata only.
The conversion/build pipeline must remove known public development keys, passwords,
old `authorized_keys`, machine-id, SSH host keys, and cloud-init cache.

For the official `dba` contract, cloud-init creates/normalizes `admin` GID 88
before `users-groups`, then creates `dba` as UID 88 with primary GID 88,
`/home/dba`, bash, locked password, and key-only passwordless sudo. A
fail-closed identity script verifies passwd/group IDs and home ownership before
disk, network, and ready-marker checks. Custom `ssh.user` values do not inherit
this Pigsty-specific numeric identity.

## Embedded formal matrix

Embedded manifest version `2026082402` contains exactly the seven formal guest
aliases `el9`, `el10`, `d12`, `d13`, `u22`, `u24`, and `u26`, with one
`amd64` and one `arm64` artifact for each alias. All 14 entries are
`status: testing`; none is represented as `supported`.

The following distribution-owned, dated URLs are the provenance inputs. The
artifact byte counts and virtual sizes were independently re-read from the
local retained corpus on 2026-08-24 using SHA-256 and
`qemu-img info --output=json -f qcow2`. Every artifact was plain qcow2 with no
backing or external data file. The embedded SHA-256 values are the complete
digests; this table focuses on source identity and sizes.

`artifact_size` is the exact number of downloaded bytes and is enforced against
both a supplied HTTPS `Content-Length` and the completed stream. `virtual_size`
is the guest-visible disk capacity reported by `qemu-img`; it is not a download
size and is re-verified after the artifact enters the managed cache.

| Alias | Release | Immutable upstream artifacts | Source user | Artifact bytes (`amd64` / `arm64`) | Virtual size | Boot |
| --- | --- | --- | --- | ---: | ---: | --- |
| `el9` | `9.8.20260525.0` | [amd64](https://dl.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base-9.8-20260525.0.x86_64.qcow2), [arm64](https://dl.rockylinux.org/pub/rocky/9/images/aarch64/Rocky-9-GenericCloud-Base-9.8-20260525.0.aarch64.qcow2) | `rocky` | 645988352 / 519831552 | 10737418240 | UEFI / UEFI |
| `el10` | `10.2.20260525.0` | [amd64](https://dl.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base-10.2-20260525.0.x86_64.qcow2), [arm64](https://dl.rockylinux.org/pub/rocky/10/images/aarch64/Rocky-10-GenericCloud-Base-10.2-20260525.0.aarch64.qcow2) | `rocky` | 544997376 / 469368832 | 10737418240 | UEFI / UEFI |
| `d12` | `20260806.2562.0` | [amd64](https://cloud.debian.org/images/cloud/bookworm/20260806-2562/debian-12-generic-amd64-20260806-2562.qcow2), [arm64](https://cloud.debian.org/images/cloud/bookworm/20260806-2562/debian-12-generic-arm64-20260806-2562.qcow2) | `debian` | 448069632 / 434044928 | 3221225472 | UEFI / UEFI |
| `d13` | `20260810.2566.0` | [amd64](https://cloud.debian.org/images/cloud/trixie/20260810-2566/debian-13-generic-amd64-20260810-2566.qcow2), [arm64](https://cloud.debian.org/images/cloud/trixie/20260810-2566/debian-13-generic-arm64-20260810-2566.qcow2) | `debian` | 436404224 / 429195264 | 3221225472 | UEFI / UEFI |
| `u22` | `20260810.0.0` | [amd64](https://cloud-images.ubuntu.com/jammy/20260810/jammy-server-cloudimg-amd64.img), [arm64](https://cloud-images.ubuntu.com/jammy/20260810/jammy-server-cloudimg-arm64.img) | `ubuntu` | 734344192 / 703484928 | 2361393152 | UEFI / UEFI |
| `u24` | `20260801.0.0` | [amd64](https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-amd64.img), [arm64](https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-arm64.img) | `ubuntu` | 624239616 / 618417664 | 3758096384 | UEFI / UEFI |
| `u26` | `20260731.0.0` | [amd64](https://cloud-images.ubuntu.com/resolute/20260731/resolute-server-cloudimg-amd64.img), [arm64](https://cloud-images.ubuntu.com/resolute/20260731/resolute-server-cloudimg-arm64.img) | `ubuntu` | 860447744 / 940920832 | 3758096384 | UEFI / UEFI |

The audit also matched the Rocky per-artifact `.CHECKSUM` byte/SHA-256
records, Ubuntu's dated [Jammy](https://cloud-images.ubuntu.com/jammy/20260810/SHA256SUMS),
[Noble](https://cloud-images.ubuntu.com/noble/20260801/SHA256SUMS), and
[Resolute](https://cloud-images.ubuntu.com/resolute/20260731/SHA256SUMS)
SHA-256 lists, and Debian's dated
[Bookworm](https://cloud.debian.org/images/cloud/bookworm/20260806-2562/SHA512SUMS)
and [Trixie](https://cloud.debian.org/images/cloud/trixie/20260810-2566/SHA512SUMS)
SHA-512 lists. The embedded SHA-256 was independently calculated over the same
local bytes.

The source families are documented by [Rocky Linux Release Engineering](https://docs.rockylinux.org/latest/teams/rel_eng/image/),
the [Debian Cloud team](https://wiki.debian.org/Cloud), and
[Ubuntu Public Images](https://documentation.ubuntu.com/public-images/).
Versioned filenames and directories are mandatory: catalog validation rejects
the moving path segments `latest`, `current`, and `release`, as well as Rocky
`.latest.` filenames. In particular, the former Debian 13 `/latest/` URL is no
longer present.

### UEFI policy

Arm64 requires UEFI. All 14 in-scope artifacts across both architectures have
a GPT and EFI System Partition, so the embedded matrix selects UEFI for
both architectures. This structural inspection proves that the required boot
material is present; it is not a substitute for a native boot smoke or host
firmware compatibility test. An x86 artifact without verified UEFI support
would need an explicit BIOS entry rather than inheriting this policy.

### Ubuntu 24.04 baseline decision

The formal `u24` entries use the standard Ubuntu Server cloud images from the
retained corpus, not the earlier Ubuntu Minimal entries. This is a deliberate
digest change:

- both standard artifacts are pinned under dated `20260801` URLs;
- the standard amd64 digest has native Linux/KVM evidence in
  [`m0-a-linux-amd64-20260824.md`](evidence/m0-a-linux-amd64-20260824.md) and
  [`m1-quick-product-linux-amd64-20260824.md`](evidence/m1-quick-product-linux-amd64-20260824.md);
- the standard arm64 digest has native macOS/HVF private evidence in
  [`m0-b-darwin-private-native-20260823.md`](evidence/m0-b-darwin-private-native-20260823.md) and
  [`m2-private-product-darwin-arm64-20260824.md`](evidence/m2-private-product-darwin-arm64-20260824.md);
- the prior Minimal URL used a moving `/release/` path and was not part of the
  retained 16-artifact formal corpus.

The Minimal-image results remain valid historical evidence for those exact
digests. They are not evidence for the new standard-image digests, and neither
variant is promoted to `supported` by this selection.

### Debian 13 baseline decision

The formal `d13` entries use the retained `generic` images from the dated
`20260810-2566` directory. They replace the former arm64 `genericcloud` entry
whose source used the moving `/latest/` directory. This changes both release
and variant, so evidence for the old digest does not transfer. Both new exact
digests subsequently passed their matching native formal smoke in the dated M4
matrices. They remain `testing` until the complete release/support and hosting
gates close.

The retained corpus check is reproducible but intentionally opt-in because it
hashes all artifacts and requires QEMU tooling:

```bash
PIGLET_IMAGE_CORPUS=/path/to/image \
  go test ./internal/image -run LocalCorpus -count=1 -v
```

An accepted image must provide:

- cloud-init NoCloud and OpenSSH server;
- writable serial console and virtio block/network support;
- root growpart and filesystem resize;
- MAC-matched management DHCP plus private static NIC without default
  route/DNS;
- XFS or ext4 tooling for data disks;
- time synchronization capable of step/catch-up after host sleep;
- creation/merge of the resolved login user with locked password, bash,
  passwordless sudo, and only the project public key.
- for official `dba`, an unoccupied/movable GID 88 and cloud-init support for
  `uid`, `primary_group`, `create_groups`, and early `bootcmd` group setup.

## CIDATA contract

The seed contains exactly `meta-data`, `user-data`, and `network-config` at the
root of an ISO9660 volume labelled `CIDATA`. A pure-Go mature library creates
the ISO. A generated instance base ID remains stable; generation changes only
when per-instance cloud-init must run again.

Readiness is `/var/lib/piglet/ready.json` containing project, node, generation,
and spec hash. It is written only after login UID/GID/home ownership, disk,
network, and optional control-key checks. The host rejects an old generation marker.

Data disks are discovered through `/dev/disk/by-id/virtio-<serial>`, formatted
only when no filesystem signature exists, and persisted in `/etc/fstab` by
filesystem UUID with `nofail`.

## Support evidence

An image cannot be marked `supported` before a real native-arch smoke. M0
requires u24, el9, and d13. GA requires each of the seven formal guest aliases
on both Tier 1 native architectures at least once, with traceable evidence.

The current 2026-08-24 UID/GID88 refresh covers all 14 in-scope exact digests,
and every one passed lifecycle/data/network/time/identity checks on its matching
Tier-1 native architecture. The historical runs also
tested EL8: amd64 passed, while arm64 failed before userspace because its fixed
64 KiB-granule kernel is unsupported by the tested Apple HVF CPU. ADR-0008
removes EL8 from the v1 target rather than allowing TCG. See
[`current Linux five`](evidence/m4-uid88-guest-refresh-linux-amd64-remaining-go127-20260824.md),
[`current Linux two`](evidence/m4-uid88-guest-refresh-linux-amd64-el9-d13-go127-20260824.md),
and [`current Darwin six`](evidence/m4-uid88-guest-refresh-darwin-arm64-go127-20260824.md),
plus [`current U24 Darwin meta`](evidence/m3-pigsty-meta-bootstrap-darwin-arm64-go127-20260824.md).
Existing
smoke evidence for a different digest or variant still does not transfer.

## Self-hosting and release gate

The embedded baseline currently points to distribution-owned upstream
artifacts. Release readiness remains gated on all of the following:

- owner-controlled immutable artifact hosting and bandwidth;
- a native run of the checked-in reproducible offline normalization pipeline;
  its validate/rejection/reproducibility boundary and real-qemu validation pass,
  but libguestfs guest mutation has not yet run on an official input;
- license facts, checksums, provenance, and SBOM/attestation where available;
- production active/standby manifest signing keys with documented offline
  custody, replacing development-only roots;
- traceable native smoke for every exact alias/architecture/digest;
- a signed manifest whose `supported` decisions are made only after those
  checks pass.

Until that gate is satisfied, distribution URLs are testing provenance inputs,
not a production availability or support promise.

The local candidate implementation is documented under
[`packaging/image-pipeline/`](../packaging/image-pipeline/README.md). It accepts
no signing secret and cannot mark an image supported. Validate-only output is
explicitly unpublishable; a successful offline no-network mutation emits a
testing manifest candidate, checksums, recipe, artifact-boundary SPDX, and
SLSA/in-toto provenance, after which repeat-build, guest-package SBOM, native
smoke, owner hosting, and production signing still remain.
