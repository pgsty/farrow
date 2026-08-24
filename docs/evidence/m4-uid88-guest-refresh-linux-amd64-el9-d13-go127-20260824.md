# Current UID/GID 88 guest refresh — Linux amd64 EL9 + D13 — 2026-08-24

Result class: **native KVM real-guest PASS** for two current guest-family
identity entries. Rocky Linux 9 and Debian 13 both completed the current
Go 1.27 `piglet-m0` create/boot/stop/start/stop contract, followed by one
bounded identity-only boot that captured the numeric account contract directly.

This is deliberately a two-entry refresh, not a claim for the complete
14-entry matrix, the public product CLI, private networking, `full`, MinIO, or
Pigsty deployment. It adds current-seed evidence for one RPM/XFS guest and one
Debian/ext4 guest. Together with the existing current U24 Darwin meta result,
3 of 14 formal native guest identity entries now have current UID/GID 88
evidence; 11 remain.

## Runner, source, and isolation

- Host: `vonng-aimax`, Ubuntu 24.04.3 LTS, kernel
  `6.17.0-35-generic`, x86_64.
- Invoking/QEMU account: UID/GID 1000 `vonng`; `/dev/kvm` is mode 0660,
  owner `root:kvm` (numeric GID 993), and was readable/writable by the runner.
- QEMU/qemu-img: 8.2.2; `q35`, KVM, CPU `host`, explicit UEFI via
  `/usr/share/OVMF/OVMF_CODE_4M.fd`.
- Build host: Darwin arm64 with Go 1.27.0. The remote host had no Go toolchain;
  it executed only the statically linked Linux/amd64 ELF.
- Binary SHA-256:
  `6d7d62a55a6485e1011e92839375968f038158d031f222f395f0fddb2bd456cf`.
  `go version -m` records `GOOS=linux`, `GOARCH=amd64`, `CGO_ENABLED=0`,
  `-trimpath=true`, and `vcs.modified=true`.
- Unique retained evidence root:
  `/data/piglet-v1-uid88-el9-d13-linux-amd64-20260824.OgzdgG`, mode 0700,
  owner `vonng:vonng`, apparent retained use 145 MiB. Each guest has its own
  work directory, key, seed, overlays, logs, and project UUID.
- The source qcows came from the prior immutable mode-0444 matrix input root.
  They were rehashed and checked before this run and rehashed afterward. No
  existing VM, overlay, project state, or shared network resource was used or
  changed.

The cross-build and each native run were equivalent to:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-buildid=' \
  -o /private/tmp/piglet-uid88-m0-linux-amd64 ./cmd/piglet-m0

ROOT=/data/piglet-v1-uid88-el9-d13-linux-amd64-20260824.OgzdgG

"$ROOT/inputs/piglet-m0" \
  --image /data/piglet-v1-guest-matrix-amd64-20260824-gjAZKB/inputs/images/el9.qcow2 \
  --sha256 92c206cc6f790c61583247eefe87890f8828420662c17cacf247cec78ab4eec8 \
  --work-dir "$ROOT/runs/el9" --boot uefi --ready-timeout 180s

"$ROOT/inputs/piglet-m0" \
  --image /data/piglet-v1-guest-matrix-amd64-20260824-gjAZKB/inputs/images/d13.qcow2 \
  --sha256 d4e6f5d1e9f571c198a65b45ab1adae6c5734607614e72f9661d84ce5881e5fc \
  --work-dir "$ROOT/runs/d13" --boot uefi --ready-timeout 180s
```

## Inputs and standard lifecycle results

| Alias | Release/input SHA-256 | Project UUID | Window (UTC) | Root/data result |
|---|---|---|---|---|
| `el9` | Rocky 9.8 / `92c206cc6f790c61583247eefe87890f8828420662c17cacf247cec78ab4eec8` | `1794cc9f-71d6-46cd-a0ec-7793ec1c1038` | 11:53:21–11:53:53 | 67,495,768,064-byte root; 68,652,367,872-byte XFS `/data` |
| `d13` | Debian 13 / `d4e6f5d1e9f571c198a65b45ab1adae6c5734607614e72f9661d84ce5881e5fc` | `1d3d51ab-05d7-4a3a-b195-1f0aed7e1d5d` | 11:55:59–11:56:20 | 67,422,650,368-byte root; 67,049,664,512-byte ext4 `/data` |

Both evidence JSON files report:

- Linux/amd64, UEFI, native KVM, two QEMU starts as invoking UID 1000;
- matching generation/spec-hash ready marker after the identity gate;
- native `x86_64`, DNS, HTTP 200 egress, synchronized NTP;
- stable virtio by-id data discovery and filesystem UUID + `nofail` fstab;
- `/data` canary persistence across stop/start;
- final `smoke=passed` and `runtime-residue=none`.

The resolved spec hash for both runs is
`160bbddd72591a8ebfb3c6073a1110534585852e62398eb9174f181b9551af3f`.
After the standard lifecycle, both root and data qcows passed a stopped-state
`qemu-img check -f qcow2` with no errors.

## Direct numeric identity proof

The ready file is created only after `/usr/local/libexec/piglet-identity-contract`
asserts `dba` UID 88, primary GID 88, `admin` GID 88, `/home/dba`, and home
ownership 88:88. To capture those facts directly rather than relying only on
that ordering, each stopped guest was booted once more from its same recorded
invocation and queried over its generated SSH key:

```bash
sudo /usr/local/libexec/piglet-identity-contract
id
getent passwd dba
getent group admin
stat -c 'home=%u:%g:%a' /home/dba
stat -c 'ssh=%u:%g:%a' /home/dba/.ssh
```

EL9 returned:

```text
login identity dba uid=88 gid=88 home=/home/dba
uid=88(dba) gid=88(admin) groups=88(admin) context=unconfined_u:unconfined_r:unconfined_t:s0-s0:c0.c1023
dba:x:88:88::/home/dba:/bin/bash
admin:x:88:
home=88:88:700
ssh=88:88:700
```

D13 returned the same numeric/filesystem contract (without an SELinux context):

```text
login identity dba uid=88 gid=88 home=/home/dba
uid=88(dba) gid=88(admin) groups=88(admin)
dba:x:88:88::/home/dba:/bin/bash
admin:x:88:
home=88:88:700
ssh=88:88:700
```

Each identity-only boot ran QEMU as UID 1000 and ended through guest
`systemctl poweroff`. Before exact cleanup, the PID, VM UUID, and root-disk
path were matched to this evidence root. Only its `qmp.sock`, `qemu.pid`, and
empty `/tmp/piglet-m0-<project-prefix>` directory were then unlinked/removed.

## Postflight and evidence integrity

Final postflight proved:

- both expected QEMU processes absent;
- both project-specific runtime directories absent;
- no listener on 2222, 15432, 13000, 18080, or 18443;
- both immutable source images still had their original SHA-256;
- four stopped root/data qcow checks passed;
- both retained private keys are mode 0600 and the evidence root is mode 0700.

Key evidence hashes:

```text
el9 evidence.json  3e81b154dbde22dd4b93a29cbdd37b7e52b7f74d95cb0bc51b0792fa9b77601d
d13 evidence.json  132b7aa646465a2b5e33c73f8d72677c7009cee1d9d65a11777de858c0cea6f7
el9 identity.txt   5d0deaa053a224ff13e0fb215dcc6251a1a2d94d5c62291c7fd7bca403199f43
d13 identity.txt   8f916bd867bfb62847d6dc0bd9c73e22fd1dbf02aa2212d7a518b5e12bab0aa8
```

`EVIDENCE_SHA256SUMS` covers every retained non-qcow evidence file and has
SHA-256
`1527878200492436a411eafa23b38e23659c41e0327dbd38901cfd2e5941e323`.
The retained directory contains private SSH keys and must remain protected.
