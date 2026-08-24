# Current UID/GID 88 guest refresh — Linux amd64 remaining five — 2026-08-24

Result class: **native KVM real-guest PASS, 5/5**. Rocky Linux 10,
Debian 12, and Ubuntu 22/24/26 each completed the current Go 1.27
`piglet-m0` create/boot/stop/start/stop contract followed by a separate bounded
boot that captured the numeric `dba`/`admin` and home/`.ssh` contract directly.

Together with the current EL9 and D13 refresh, this closes the current
Linux/amd64 numeric identity set at 7/7 formal aliases. Including the current
U24 Darwin meta result, 8/14 formal native guest identity entries now have
current UID/GID 88 evidence; the other six arm64 entries remain. This is not a
private-network, multi-node, `full`, MinIO, Pigsty-deployment, packaging,
signing, or release-publication result.

## Runner, source, and isolation

- Host: `vonng-aimax`, Ubuntu 24.04.3 LTS, kernel
  `6.17.0-35-generic`, x86_64.
- Invoking and QEMU account: UID/GID 1000 `vonng`; `/dev/kvm` was mode 0660,
  owner `root:kvm` (numeric GID 993), and readable/writable by the runner.
- QEMU/qemu-img 8.2.2; `q35`, native KVM, CPU `host`, UEFI through
  `/usr/share/OVMF/OVMF_CODE_4M.fd` and per-run NVRAM.
- Build host: Darwin arm64 with Go 1.27.0. The current source was cross-built
  as a static Linux/amd64 ELF with `CGO_ENABLED=0`, `-trimpath=true`, and an
  empty build ID. `go version -m` truthfully records the development module
  and `vcs.modified=true`.
- Harness SHA-256:
  `a04e5f2d0df4f104b087e10e5b09b274d5027b48a69055181f5b5d16eb29a2c4`.
- Unique retained root:
  `/data/piglet-v1-uid88-remaining-linux-amd64-20260824.WFQabK`, mode 0700,
  owner 1000:1000, with 399 MiB apparent retained use. It contains generated
  private keys and remains sensitive.
- The driver ran exactly one guest at a time. Every pre-entry check required
  no QEMU or `piglet-m0` process and no listener on the five reserved
  loopback ports. It used QEMU user networking only and did not change host
  networking or inspect, stop, or modify an existing VM.

The build was equivalent to:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-buildid=' \
  -o /private/tmp/piglet-uid88-remaining-linux-amd64.QlFUkZ \
  ./cmd/piglet-m0
```

After the remote copy was verified, that exact local temporary binary was
unlinked. The replay drivers and executable remain protected under the
retained evidence root.

## Exact manifest inputs

All five exact inputs were already present under the retained earlier matrix
root
`/data/piglet-v1-guest-matrix-amd64-20260824-gjAZKB/inputs/images`.
No qcow was transferred. Each source was a real regular file, mode 0444,
owner 1000:1000, with the embedded manifest byte count and SHA-256.

| Alias | Release | Artifact bytes | SHA-256 |
|---|---|---:|---|
| `el10` | `10.2.20260525.0` | 544,997,376 | `9fc9e9ff16888bb68ac39b0392e25c9c92684d50c85f1cce6ab549363bbc4b48` |
| `d12` | `20260806.2562.0` | 448,069,632 | `dd3dbd23a3965318cc9aae32592dcfde4abcb8f90a50ca760a9ca9e8f3ba6255` |
| `u22` | `20260810.0.0` | 734,344,192 | `6de0c42a98dc9a749917dfef34bf54e3595441bf67d39f103a61341560b3da8e` |
| `u24` | `20260801.0.0` | 624,239,616 | `0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe` |
| `u26` | `20260731.0.0` | 860,447,744 | `9dc7c5363c0146a08ba0c9aa834d82c2c6dfbb1c471ad9a2f0aba1189e21be05` |

Before launch, `qemu-img info --output=json --force-share -f qcow2` proved
the declared virtual size, qcow2 format, and no backing file, external data
file, or encryption for 5/5 inputs. `qemu-img check --output=json -f qcow2`
reported zero corruption, leaks, and check errors for all five. The source
digests were re-read after the complete run and matched 5/5 again.

Each lifecycle command had this shape:

```bash
ROOT=/data/piglet-v1-uid88-remaining-linux-amd64-20260824.WFQabK

"$ROOT/inputs/piglet-m0" \
  --image /data/piglet-v1-guest-matrix-amd64-20260824-gjAZKB/inputs/images/<alias>.qcow2 \
  --sha256 <embedded-manifest-digest> \
  --work-dir "$ROOT/runs/<alias>" \
  --boot uefi \
  --ready-timeout 300s
```

## Lifecycle results

Times are UTC and cover the standard two-start lifecycle. Paths are relative
to the retained evidence root. Every evidence JSON records two QEMU starts as
UID 1000, native KVM/`host`, matching generation/spec-hash readiness, native
`x86_64`, DNS, HTTP 200 egress, synchronized NTP, persistent `/data`, final
`smoke=passed`, and `runtime-residue=none`.

| Alias | Project UUID | Window | Root/data observation | Evidence JSON SHA-256 |
|---|---|---|---|---|
| `el10` | `8d5daf3a-7691-4fb9-acaa-24f31076a1d7` | 12:39:56–12:40:29 | 66,985,238,528-byte root; 68,652,367,872-byte XFS `/data` | `541382190a8cd1ca9456423163bd963078f0d6eb55727e59602e6a757cd2eefc` |
| `d12` | `76be846d-3e9b-430c-b3e3-0b9171737dfe` | 12:40:40–12:41:10 | 67,422,650,368-byte root; 67,049,664,512-byte ext4 `/data` | `34d0bd914c166c39bcec3c42709e076da054b4796e0909dcb9d11367b71f50d7` |
| `u22` | `69441068-12bf-4a12-8eae-d3d155f110f7` | 12:43:07–12:43:37 | 66,404,147,200-byte root; 68,685,922,304-byte XFS `/data` | `3bdf71cf193fdd983b06d10461f9c7789a9bb2667fc68ce005f70363dece0b58` |
| `u24` | `c83e4b36-ab92-446f-8e42-749eec9d9625` | 12:43:47–12:44:15 | 65,445,814,272-byte root; 68,652,367,872-byte XFS `/data` | `48a396ea3954fdfc423965e178a66b9c371234533cbff8e153734907a01a8c61` |
| `u26` | `82e0ed86-7894-4e62-8c0e-623b67ae54f8` | 12:44:28–12:47:09 | 65,421,303,808-byte root; 68,652,367,872-byte XFS `/data` | `c489237666af1debcf33addfa78d51cdf26232215210201c627a48f072760735` |

The resolved spec hash was
`160bbddd72591a8ebfb3c6073a1110534585852e62398eb9174f181b9551af3f`
for all five entries. U26 spent most of its 160.5-second lifecycle in its known
guest-side network wait and completed inside the unchanged 300-second bound.

## Direct numeric identity proof

After each standard final stop, the same recorded UEFI invocation was started
once more. `/proc/<pid>/cmdline`, the project UUID, evidence-root root disk,
QEMU executable, `-accel kvm`, and `-cpu host` were captured, and the process
UID was 1000 in all five cases. The generated key then queried the guest
directly:

```text
sudo /usr/local/libexec/piglet-identity-contract
id
getent passwd dba
getent group admin
stat -c 'home=%u:%g:%a' /home/dba
stat -c 'ssh=%u:%g:%a' /home/dba/.ssh
id -u
id -g
```

Every entry returned the following numeric/account facts:

```text
login identity dba uid=88 gid=88 home=/home/dba
uid=88(dba) gid=88(admin) groups=88(admin)
dba:x:88:88::/home/dba:/bin/bash
admin:x:88:
numeric_uid=88
numeric_gid=88
```

EL10 additionally reported its SELinux context on the `id` line. Direct
filesystem ownership and evidence hashes were:

| Alias | `/home/dba` | `/home/dba/.ssh` | Identity text SHA-256 |
|---|---|---|---|
| `el10` | `88:88:0700` | `88:88:0700` | `395e0b72e19c77f6cf12d6a6df81ca8a52576a5f427043129649de152762da75` |
| `d12` | `88:88:0755` | `88:88:0700` | `41adb7e5e0a380c6929ece243775b8432c73909845201ce2764a7a13772711a1` |
| `u22` | `88:88:0750` | `88:88:0700` | `06e95b4e7576c00ad05708a35af3897612d0c4883eac68f6089eb6a0b83ffa97` |
| `u24` | `88:88:0750` | `88:88:0700` | `06e95b4e7576c00ad05708a35af3897612d0c4883eac68f6089eb6a0b83ffa97` |
| `u26` | `88:88:0750` | `88:88:0700` | `06e95b4e7576c00ad05708a35af3897612d0c4883eac68f6089eb6a0b83ffa97` |

The formal identity gate requires the numeric passwd/group identity and home
ownership; it does not impose one cross-distribution home-directory mode.

## Bounded audit correction

The first driver incorrectly added a host-side assertion that `/home/dba`
must be mode 0700. It stopped after D12 had already returned the complete
successful direct proof above, leaving only that exact identity-proof QEMU
active. No guest lifecycle or identity command failed.

The retained recovery procedure matched the live process against QEMU UID
1000, `/usr/bin/qemu-system-x86_64`, KVM/host arguments, D12 project UUID,
evidence root, root-disk path, PID file, and runtime directory before issuing
guest `systemctl poweroff`. It then owner- and allowlist-checked only
`/tmp/piglet-m0-76be846d/{qmp.sock,qemu.pid}`, unlinked those exact stopped
runtime artifacts, removed that empty directory, and rechecked both qcows.
The resume preserved EL10/D12 results, changed the audit to require home
ownership while retaining the observed mode, and ran only U22/U24/U26.

## Postflight and evidence integrity

Final independent read-only audit proved:

- all five immutable source SHA-256 values still matched the manifest;
- all ten stopped generated root/data qcows had zero corruption, leak, or
  check errors after the direct identity boots;
- no QEMU or `piglet-m0` process remained;
- no listener remained on 2222, 15432, 13000, 18080, or 18443;
- all five project-specific `/tmp/piglet-m0-<prefix>` directories were absent;
- every run directory was mode 0700 and every retained private key mode 0600.

Only verified project-specific `qmp.sock`/`qemu.pid` files and their empty
runtime directories were cleaned. Source images, generated qcows, keys,
seeds, NVRAM, serial logs, process captures, identity output, recovery record,
and drivers remain protected under the evidence root.

Key aggregate hashes:

```text
logs/results.tsv           78c1832bdbd8468991797742d0d2ee7441a463ca5a20a4826707e54285c5a587
logs/input-audit.tsv       6a1f0fac010e8623fc4071ab47822b17d26e719253d3cc2f76c41a1cbc9fbff6
logs/input-sha256-after.tsv 27b34355d92d84fb4b80f2b8352528f3be631767d8c11ab33beab9ae469a86e4
```

`EVIDENCE_SHA256SUMS` covers all 163 retained non-qcow evidence files. A
fresh `sha256sum -c` passed 163/163; the manifest SHA-256 is:

```text
2b49d7cb5f4c1fd90adfc89f94eaebda54f855a7e81a480c32629f790d9e9b02
```
