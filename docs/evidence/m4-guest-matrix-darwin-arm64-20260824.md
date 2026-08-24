# M4 formal guest periodic smoke — Darwin arm64 — 2026-08-24

Result class: **native periodic user-network smoke, 7/8 passed**. Seven exact
formal arm64 artifacts completed a real HVF create/boot/SSH/stop/start/stop
lifecycle. The formal Rocky Linux 8 arm64 artifact failed before Linux started
because its 64 KiB-granule kernel is incompatible with the CPU exposed by HVF
on this host.

This is not private-network or full-profile evidence. It does not exercise
socket_vmnet, fixed private IPs, VM-to-VM traffic, control-node lateral SSH, or
multi-node orchestration, and it does not promote any manifest entry from
`testing` to `supported`.

## Host, binary, and retained root

- Host: macOS 26.5.2 (25F84), Darwin 25.5.0, arm64 T6000, invoking UID 501.
- QEMU/qemu-img: Homebrew 11.1.0.
- Accelerator/machine/CPU: HVF, `virt`, `host`; guest architecture expected
  and observed as `aarch64` for every passing entry.
- Firmware: `/opt/homebrew/share/qemu/edk2-aarch64-code.fd` with per-VM copies
  of `/opt/homebrew/share/qemu/edk2-arm-vars.fd`.
- Harness: `/Users/vonng/pgsty/piglet/bin/piglet-m0`, SHA-256
  `2710609d943f2f578a493c7c52d1ea919dd50bdc87c4bbc1c8d0f7bc66761709`.
- Maximum concurrency: two VMs, each with 2 vCPU and 4 GiB memory.
- Readiness timeout: 300 seconds per entry.
- Retained evidence root:
  `/Users/vonng/Library/Caches/piglet/guest-matrix-formal-arm64-20260824-AmRGXX`.
- Root mode/owner: `0700`, UID 501; retained size: 6.6 GiB.

The retained root is sensitive. Each run contains a mode-0600 private SSH key,
known-host state, CIDATA seed, serial log, NVRAM, and guest root/data disks.
Do not publish or relax permissions on this directory.

## Input and preflight contract

The eight arm64 rows were selected from
`/Users/vonng/pgsty/cache/image/manifest.tsv`. For each input, the test
independently verified:

- release, relative path, byte count, and SHA-256 against `manifest.tsv`;
- the same digest/path pair against `SHA256SUMS`;
- actual file SHA-256 and byte count;
- `qemu-img info --output=json --force-share -f qcow2`: qcow2, expected
  virtual size, no backing/data-file/encryption, not dirty/corrupt;
- `qemu-img check --output=json -f qcow2`: exit 0 with no corruption, leak, or
  check error.

The original corpus files were mode 0644 and were not modified. The harness
used same-byte APFS clones under `inputs/`, changed to mode 0444 because the M0
harness requires an immutable base. Preflight details are in
`preflight/inputs.tsv`, SHA-256
`53221afea581a9ae873c46a5e7085bf584e8ca252c65ac3513fd476b18536ac7`.

The effective command shape for every entry was:

```text
bin/piglet-m0 \
  --image <retained-root>/inputs/<alias>.qcow2 \
  --sha256 <manifest digest> \
  --work-dir <retained-root>/runs/<alias> \
  --ready-timeout 300s
```

## Matrix result

Times are UTC. Evidence paths below are relative to the retained root.
`root/data clean` means both retained runtime qcow2 files passed a post-stop
read-only `qemu-img info` and `qemu-img check` audit.

| Alias | Release | Input SHA-256 | Boot/lifecycle result | Evidence path and SHA-256 | Final qemu-img | Evidence window |
| --- | --- | --- | --- | --- | --- | --- |
| `el8` | `8.10.20240528.0` | `946b5b9845aa5e3ed98f1bc6ee9873201712a2aef01b87731aed16857e0ca13f` | **FAIL**: 64 KiB-granule kernel unsupported by HVF host CPU; no SSH/ready marker | `runs/el8/evidence.json` — `8c031f8533474fb688a3a102456c061de6ac57c30a2d1b43b1078718eb8e51bf` | root/data clean | 19:15:42.462–19:20:44.621 (302.6s) |
| `el9` | `9.8.20260525.0` | `24692a444f1f0b8bb95375c38c8b43f8099a115347623691be2c330b40c8a1fe` | **PASS** | `runs/el9/evidence.json` — `74012fb8c844e16887ef04ba3f6f6f96093ba5340d268667a045a6acd6cdaa2c` | root/data clean | 19:15:42.462–19:16:43.382 (61.4s) |
| `el10` | `10.2.20260525.0` | `457c8375e19496f43a25c4a6169fa11237536c53cef6f85a20ea3c5a751aa0f5` | **PASS** | `runs/el10/evidence.json` — `84dd4990e1b5ae92852aebbc5b8aae4931425a158fb1330ab7001f745136f47c` | root/data clean | 19:16:43.427–19:17:52.208 (68.8s) |
| `d12` | `20260806.2562.0` | `8c6b8f81e571d530f6561c707538a4e807de8188c9a3f41af7b52b4e5ed010be` | **PASS** | `runs/d12/evidence.json` — `8daa70a31dbc947ee130020df3878d398a8c791cb12c4d00f4b1e9486cd41098` | root/data clean | 19:17:52.227–19:18:23.358 (31.2s) |
| `d13` | `20260810.2566.0` | `2c546c79ec199983a88e384f6e5d013ab7876353943f7aa614403e3028bbea99` | **PASS** | `runs/d13/evidence.json` — `658ca870f98d2e0924c6e539e0202368547a404ee4a7982eff5f611ccd94ead1` | root/data clean | 19:18:23.377–19:18:57.106 (33.7s) |
| `u22` | `20260810.0.0` | `b57a88a8d3b9f33d48f1b3d70a1aac7ae79760c9b507699d2601989eadac02b1` | **PASS** | `runs/u22/evidence.json` — `36cc372c3e4c70f70f980b81bc3c070ab55727e7251e1a798ad7f4632777dd9e` | root/data clean | 19:18:57.121–19:19:42.790 (45.7s) |
| `u24` | `20260801.0.0` | `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476` | **PASS** | `runs/u24/evidence.json` — `32d3af9632665170e108c33ef96c4c776c6ff9c0225a59b8a9eca593d49fb903` | root/data clean | 19:19:42.815–19:20:24.592 (41.8s) |
| `u26` | `20260731.0.0` | `3e113fdd41f39e13729375173bb2ae793f87dc6db4294e5251ff2476971788ba` | **PASS** after a long networkd wait-online delay | `runs/u26/evidence.json` — `f5df527e3e3dde298aae4ca18cf63262f9ed65bf713651588aa9a7ac265a9da1` | root/data clean | 19:20:24.613–19:23:17.086 (172.5s) |

Driver results are preserved at `driver/results.tsv`, SHA-256
`e786739132a6b019b85a6802071bbd4760be2ac1005fafbecbeb6b1595b64168`.

## Passing lifecycle observations

Every passing exact digest verified:

- native `aarch64`, cloud-init generation/spec-hash readiness, and SSH as
  `dba`;
- root filesystem growth beyond 60 GiB;
- a 64 GiB `/data` disk resolved through its stable virtio by-id serial;
- XFS or ext4 initialization, filesystem UUID plus `nofail` in fstab, and a
  persistence canary surviving stop/start;
- DNS plus outbound HTTP 200 and NTP synchronization;
- QMP-identified start/stop/start/stop with two captured QEMU processes, both
  running as UID 501;
- final harness result `passed` and no owned runtime residue.

Observed `/data` filesystems were XFS on `el9`, `el10`, `u22`, `u24`, and
`u26`, and ext4 on `d12` and `d13`.

## Rocky Linux 8 failure diagnosis

The Rocky Linux 8 UEFI image reached GRUB and selected its aarch64 kernel, but
the serial console then reported:

```text
EFI stub: ERROR: This 64 KB granular kernel is not supported by your CPU
Failed to boot both default and fallback entries.
```

The harness consequently timed out waiting for SSH and
`/var/lib/piglet/ready.json`. This is a native image/host compatibility
failure, not a checksum, qcow2, firmware-discovery, or harness-directory
failure. Falling back to TCG or another CPU model would no longer be the
required native HVF/host-CPU smoke, so no such fallback was attempted. The
failed evidence, serial log, disks, NVRAM, key material, and driver logs remain
untouched under `runs/el8`.

The failed harness left only an empty owned mode-0700 runtime directory after
stopping QEMU. It was moved intact from `/tmp/piglet-m0-2aa1eb90` to
`runs/el8/runtime-residue`, with an origin marker, so the failure context is
preserved without leaving host runtime state.

## Postflight integrity and residue audit

All 16 generated disks, including the failed el8 root/data pair, passed final
read-only `qemu-img info/check`. Per-disk JSON is retained under `postflight/`.
The postflight matrix summary is `postflight/results.tsv`, SHA-256
`09a8ecf78eb9cefe64631cb9e6f3d4a6277031bc4d3b659afc6ff33edeff6487`.

The final host audit found:

- no `qemu-system-aarch64` or `piglet-m0` process;
- no listener on any SSH/business forward used by the matrix;
- no `/tmp/piglet-m0-<project>` directory for any of the eight projects and
  no global `piglet-m0-*` runtime directory;
- all eight run directories private and all eight host private keys mode 0600.

The replayable residue record is `postflight/residue.txt`, SHA-256
`c02f658ed2c3808514e1812ede068d7015a9eae70ea5a81f7f4aa801af60414c`.

## Evidence boundary

This run is the Darwin/arm64 periodic smoke for the exact formal artifacts
listed above. It supplies positive evidence for seven exact digests and a
reproducible negative result for el8/arm64 on this Tier 1 host. It is not:

- Linux/amd64 evidence;
- a private/full/multi-node profile run;
- an all-host compatibility claim;
- permission to label any artifact `supported` without the remaining native
  matrix, provenance, self-hosting, and signing gates.
