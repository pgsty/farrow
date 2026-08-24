# Current UID/GID 88 guest refresh — Darwin arm64 remaining six — 2026-08-24

Result class: **native HVF real-guest PASS** for the six remaining current
Darwin/arm64 formal guest identity entries. EL9, EL10, Debian 12, Debian 13,
Ubuntu 22.04, and Ubuntu 26.04 each completed the current Go 1.27
`piglet-m0` create/boot/stop/start/stop lifecycle and a bounded direct numeric
identity probe. Read with the already-current U24 Darwin result, this closes
the seven in-scope Darwin/arm64 UID/GID 88 entries.

This is not a U24 rerun, Linux evidence, private networking, `full`, MinIO, or
Pigsty deployment evidence. It does not change any manifest support status.

## Runner, binary, and guarded evidence

- Host: macOS 26.5.2 (25F84), Darwin 25.5.0, arm64 T6000.
- Invoking and QEMU account: UID 501 `vonng`; no root QEMU process was used.
- QEMU/qemu-img: Homebrew 11.1.0; machine `virt`, accelerator HVF, CPU `host`,
  native `aarch64` guest architecture, and UEFI.
- Build: Go 1.27.0, `GOOS=darwin`, `GOARCH=arm64`, `CGO_ENABLED=0`,
  `-trimpath=true`, and an empty build ID, from the current
  `./cmd/piglet-m0` source.
- Binary SHA-256:
  `f54800fbe27adf0d3f29d3b8093f0cae6f383af08a53e638a01877f59747f0c6`.
- Retained evidence root:
  `/Users/vonng/Library/Caches/piglet/uid88-formal-darwin-arm64-go127-20260824.eg1QRn`.
  It is mode 0700, owned by UID 501, and retained about 4.45 GB of apparent
  disk use at final audit.
- Maximum concurrency: one guest. Every entry released QEMU and all five
  loopback forwards before the next entry started.

The retained root contains six private SSH keys, known-host state, seeds,
NVRAM, serial logs, source clones, and generated VM disks. It is sensitive and
must remain mode 0700.

## Exact manifest inputs

Each source was selected as the single `arm64` row for its alias from
`/Users/vonng/pgsty/cache/image/manifest.tsv`. Preflight independently matched
the path, byte count, digest, and `SHA256SUMS` row; ran read-only `qemu-img
info/check`; and rejected backing files, data files, encryption, dirty state,
and corruption. Because M0 requires an immutable base, it used same-byte APFS
clones retained under `inputs/` at mode 0444. The mode-0644 corpus sources
were never changed.

| Alias | Release | Corpus file | Bytes | SHA-256 |
|---|---|---|---:|---|
| `el9` | `9.8.20260525.0` | `el9/Rocky-9-GenericCloud-Base-9.8-20260525.0.aarch64.qcow2` | 519,831,552 | `24692a444f1f0b8bb95375c38c8b43f8099a115347623691be2c330b40c8a1fe` |
| `el10` | `10.2.20260525.0` | `el10/Rocky-10-GenericCloud-Base-10.2-20260525.0.aarch64.qcow2` | 469,368,832 | `457c8375e19496f43a25c4a6169fa11237536c53cef6f85a20ea3c5a751aa0f5` |
| `d12` | `20260806.2562.0` | `d12/debian-12-generic-arm64-20260806-2562.qcow2` | 434,044,928 | `8c6b8f81e571d530f6561c707538a4e807de8188c9a3f41af7b52b4e5ed010be` |
| `d13` | `20260810.2566.0` | `d13/debian-13-generic-arm64-20260810-2566.qcow2` | 429,195,264 | `2c546c79ec199983a88e384f6e5d013ab7876353943f7aa614403e3028bbea99` |
| `u22` | `20260810.0.0` | `u22/jammy-server-cloudimg-arm64.img` | 703,484,928 | `b57a88a8d3b9f33d48f1b3d70a1aac7ae79760c9b507699d2601989eadac02b1` |
| `u26` | `20260731.0.0` | `u26/resolute-server-cloudimg-arm64.img` | 940,920,832 | `3e113fdd41f39e13729375173bb2ae793f87dc6db4294e5251ff2476971788ba` |

The effective command shape, run serially for each alias, was:

```bash
ROOT=/Users/vonng/Library/Caches/piglet/uid88-formal-darwin-arm64-go127-20260824.eg1QRn

"$ROOT/inputs/piglet-m0" \
  --image "$ROOT/inputs/<alias>.qcow2" \
  --sha256 <manifest-digest> \
  --work-dir "$ROOT/runs/<alias>" \
  --ready-timeout 300s
```

## Lifecycle results

Times are UTC and cover the formal M0 run. Each evidence file ends with
`smoke=passed` and `runtime-residue=none`.

| Alias | Project UUID | Window | Evidence SHA-256 | Direct identity SHA-256 |
|---|---|---|---|---|
| `el9` | `1b0cf624-9b7a-4e46-acee-9f9ce8049a71` | 12:40:48–12:41:35 | `43583416e76d98c17cc166b170234cf8850718e163c61bb31a559d274193b16b` | `5d0deaa053a224ff13e0fb215dcc6251a1a2d94d5c62291c7fd7bca403199f43` |
| `el10` | `07644da5-497f-45da-8d67-7f0d8e283c82` | 12:42:08–12:42:59 | `dedc9c5d7e41f8fb7bf3f02d8975e41f2202b7cafe1b8c20f102c2be53e6f701` | `5d0deaa053a224ff13e0fb215dcc6251a1a2d94d5c62291c7fd7bca403199f43` |
| `d12` | `34294753-e8a7-4b8c-bdfd-4dcbb4e2aa8f` | 12:43:43–12:44:17 | `00800f019339214fb94d73795bf809e7f28b310b5723edadde4985eae430b346` | `8d45e9e062e892e16b879ac19cc64ce5f51b642ed7a0d168282b38358bdfc45d` |
| `d13` | `37eef1a6-cb8f-4d59-98ef-b8a72c4a0ad4` | 12:46:01–12:46:34 | `6a2357272c1b636cd8eca7b4ca37cbcbd5fb986db6512e52b480386fc5180058` | `8f916bd867bfb62847d6dc0bd9c73e22fd1dbf02aa2212d7a518b5e12bab0aa8` |
| `u22` | `98a1fa9b-628c-4331-b605-96b7bcc84e49` | 12:47:05–12:47:46 | `810315fa8efbedaf7f88c060808d9337bf99e86e6a3786464867180cb98a0874` | `dd1c7db62a6ab8b954549ddc956f16737a5db46b00c8439651a90f9f5a96b535` |
| `u26` | `40d492b7-cdad-4099-9996-4108d2c65d51` | 12:49:12–12:52:03 | `8fd074ed2ee6bee8c308425e7b67ea837191c74fba709cb8c2989b29495c61b7` | `dd1c7db62a6ab8b954549ddc956f16737a5db46b00c8439651a90f9f5a96b535` |

All six formal runs recorded:

- matching generation and spec-hash readiness after the identity gate;
- native `aarch64`, two QEMU starts as UID 501, and invocation arguments
  `-machine virt -accel hvf -cpu host` with no TCG fallback;
- root growth beyond 60 GiB and a 64 GiB `/data` filesystem discovered by
  stable virtio by-id serial;
- UUID plus `nofail` fstab state, DNS, HTTP 200 egress, NTP synchronization,
  and `/data` canary persistence across stop/start;
- XFS `/data` on EL9, EL10, U22, and U26; ext4 on D12 and D13; and
- the same resolved spec hash,
  `160bbddd72591a8ebfb3c6073a1110534585852e62398eb9174f181b9551af3f`.

## Direct numeric identity proof

M0 writes readiness only after `/usr/local/libexec/piglet-identity-contract`
passes, but its evidence JSON does not serialize the numeric passwd/group and
directory facts. Each stopped guest was therefore booted from its recorded
invocation once more and queried with its generated key:

```bash
sudo /usr/local/libexec/piglet-identity-contract
id
getent passwd dba
getent group admin
stat -c 'home=%u:%g:%a' /home/dba
stat -c 'ssh=%u:%g:%a' /home/dba/.ssh
```

Every guest returned the same account and group contract:

```text
login identity dba uid=88 gid=88 home=/home/dba
uid=88(dba) gid=88(admin) groups=88(admin)
dba:x:88:88::/home/dba:/bin/bash
admin:x:88:
```

EL9 and EL10 additionally included their SELinux context on the `id` line.
Direct directory results were:

| Alias | `/home/dba` owner:mode | `/home/dba/.ssh` owner:mode | Identity-probe QEMU |
|---|---|---|---|
| `el9` | `88:88:0700` | `88:88:0700` | UID 501, HVF/host |
| `el10` | `88:88:0700` | `88:88:0700` | UID 501, HVF/host |
| `d12` | `88:88:0755` | `88:88:0700` | UID 501, HVF/host |
| `d13` | `88:88:0700` | `88:88:0700` | UID 501, HVF/host |
| `u22` | `88:88:0750` | `88:88:0700` | UID 501, HVF/host |
| `u26` | `88:88:0750` | `88:88:0700` | UID 501, HVF/host |

The distro-specific home modes are not identity failures: the implemented
readiness contract requires `/home/dba` to be a real directory owned 88:88,
while the generated `.ssh` directory is explicitly mode 0700. An initial
local evidence assertion incorrectly required a narrower home-mode allowlist
for D12 and U22. Their numeric proof had already passed and been captured; the
identity-only QEMU process was then terminated only after its PID, UUID, root
path, UID 501, and HVF invocation matched. This did not affect their already
complete formal create/stop/start/stop result. The exact diagnostic and cleanup
notes are retained with those two runs. The other four identity-only boots
ended through guest `systemctl poweroff`.

## Stopped-state and residue postflight

Final matrix-wide postflight ran after every guest was stopped and proved:

- all six original corpus paths and all six mode-0444 clones still matched
  their manifest SHA-256;
- all 12 generated root/data qcows reported zero check errors, corruptions,
  and leaks with `qemu-img check -f qcow2`;
- no `qemu-system-aarch64` or `piglet-m0` process, no listener on 2222, 13000,
  15432, 18080, or 18443, and no global `/tmp/piglet-m0-*` directory remained;
- all six retained run directories were mode 0700 and private keys mode 0600;
  and
- all 12 formal QEMU processes plus all six identity-probe processes were
  captured as UID 501 with native HVF/host invocation.

Key retained-file hashes:

```text
EVIDENCE_SHA256SUMS                 708e1ce7f5a34af20519a7bc94582376423a4b111db638f4cc14864a90f5954d
driver/results.tsv                  2bbd0ff0571a188a0d4609b0a13dfea9f909b20dbf3215dcdfbf746aa2311618
preflight/inputs.tsv                113d5fb8fc8b10bc603e3ee8bbc8389ac28ad393a3edadc8472342045aa3db89
postflight/final-source-digests.tsv a733a6c5931f9104801404d8186177db28e27a9e74ba53f9cbfd63b25a4aced1
postflight/final-disks.tsv          e82e9168aa63fd54674c4dd738fd5365f372d994a23796602a7e49a2298fbc1c
postflight/final-identities.tsv     b5fe7a95776ec2029995a4bb487c3b749ed9104fa0f040ae8adfe809918b3eed
postflight/final-residue.txt        933964d324c5de0964442f5cff6f1cf4ba52fa42b7238db784c2f7d0f77596bc
```

`EVIDENCE_SHA256SUMS` covers 155 retained non-qcow evidence files, excluding
itself. Generated qcows are covered by the stopped-state structural/check
records; immutable inputs are additionally bound to the manifest digests.

## Evidence boundary

This run refreshes only the six named Darwin/arm64 formal aliases against the
current UID/GID 88 seed and Go 1.27 M0. It makes no claim for U24 beyond its
separate current evidence, any Linux entry, private network semantics,
multi-node profiles, image provenance/custody, or production support.
