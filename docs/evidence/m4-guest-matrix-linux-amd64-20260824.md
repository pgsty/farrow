# M4 formal guest matrix — Linux amd64 — 2026-08-24

Result class: **native real guest E2E** for all eight formal Linux/amd64 guest
entries. Each final entry completed native KVM create/boot, SSH and cloud-init
readiness, root growth, data-disk identity/format/mount, DNS/outbound HTTP, NTP,
stop/start persistence, final stop, and no runtime residue.

This matrix uses the replayable `piglet-m0` quick harness. It is not a
private-network, four-node `full`, Pigsty bootstrap, packaging, signing, or
release-publication result.

## Runner and retained evidence root

- Host: `vonng-aimax`, Ubuntu 24.04, kernel
  `6.17.0-35-generic`, x86_64.
- Invoking account and QEMU identity: UID/GID 1000 `vonng`; `/dev/kvm`
  readable/writable; every captured QEMU process UID 1000.
- QEMU/qemu-img: Ubuntu QEMU 8.2.2, `q35`, KVM, CPU `host`.
- Formal boot contract: UEFI with
  `/usr/share/OVMF/OVMF_CODE_4M.fd` and a per-run copied vars file.
- Execution window: initial matrix
  `2026-08-23T19:18:07Z` through `19:33:10Z`, UEFI retries through
  `19:35:04Z`, final audit `19:37:12Z`
  (`2026-08-24 03:18–03:37` Asia/Shanghai).
- Retained root:
  `/data/piglet-v1-guest-matrix-amd64-20260824-gjAZKB`, mode 0700,
  owner `vonng:vonng`, final size 6.7 GiB.

The harness was run strictly sequentially, so concurrency was one and no
user-NAT port-allocation race was possible. Every input and artifact directory
is under the unique evidence root. Existing cache/source files were read only;
no old project or `/data/pgsty/piglet` path was modified.

## Source snapshot and boot-mode correction

The first Linux amd64 `piglet-m0` was cross-built with Go 1.26.7 after two
byte-identical snapshots of `cmd/`, `internal/`, `go.mod`, and `go.sum`:

```text
initial piglet-m0
71da9902bbbf413849776a05cf8a7bc0b80cbf2ee81ac54c2f30e981e48b5eb7
```

That binary predated typed image boot selection. On Linux amd64 it used BIOS;
this let el8 pass but made the formal UEFI-only el9/el10 images silent and
unbootable. The parent agent added typed `--boot auto|bios|uefi` and supplied a
new current-source static Linux amd64 binary. Its transferred digest was
verified before use:

```text
UEFI-capable piglet-m0
bb39d89794ba6ff665584d7baeb72511dc5dddb08017ce30587ad5b146d373b9
```

The already-running el10 BIOS attempt was allowed to finish naturally. The old
executable inode was retained as `inputs/piglet-m0-bios-initial`; a fixed
mode-0555 wrapper at the original loop path invoked the new binary with
`--boot uefi`. Its SHA-256 is
`63997f282d3d811441ae75f0c713dc1359728fe01d3440f3b18aa364cc792263`.
Subsequent runs therefore used UEFI without interrupting or overwriting the
active failure evidence. El8/el9/el10 were then independently rerun in new UEFI
directories with the new binary directly.

The repository has no commit, so both builds truthfully identify a modified
development tree. Evidence is bound to exact binary/source-input hashes rather
than an invented revision.

## Input provenance and qcow2 validation

The local source was `/Users/vonng/pgsty/cache/image`, whose `manifest.tsv` and
`SHA256SUMS` record release, architecture, source URL, size, and digest. The
remote host already had only the u24 digest in a mode-0444 old cache; that file
was copied read-only into the new root. The other seven local amd64 files were
transferred into `inputs/images/`. Old directories were never modified.

Every new input was changed to mode 0444 and then independently rehashed on the
runner. `qemu-img info --output=json -f qcow2` proved plain qcow2, no backing or
data file, no encryption, and a clean header. `qemu-img check -f qcow2` passed
8/8 before any VM launch.

| Alias | Release | SHA-256 | Artifact bytes | Virtual size |
|---|---|---|---:|---:|
| el8 | 8.10.20240528.0 | `e56066c58606191e96184de9a9183a3af33c59bcbd8740d8b10ca054a7a89c14` | 2,065,760,256 | 10 GiB |
| el9 | 9.8.20260525.0 | `92c206cc6f790c61583247eefe87890f8828420662c17cacf247cec78ab4eec8` | 645,988,352 | 10 GiB |
| el10 | 10.2.20260525.0 | `9fc9e9ff16888bb68ac39b0392e25c9c92684d50c85f1cce6ab549363bbc4b48` | 544,997,376 | 10 GiB |
| d12 | 20260806.2562.0 | `dd3dbd23a3965318cc9aae32592dcfde4abcb8f90a50ca760a9ca9e8f3ba6255` | 448,069,632 | 3 GiB |
| d13 | 20260810.2566.0 | `d4e6f5d1e9f571c198a65b45ab1adae6c5734607614e72f9661d84ce5881e5fc` | 436,404,224 | 3 GiB |
| u22 | 20260810.0.0 | `6de0c42a98dc9a749917dfef34bf54e3595441bf67d39f103a61341560b3da8e` | 734,344,192 | 2,361,393,152 bytes |
| u24 | 20260801.0.0 | `0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe` | 624,239,616 | 3.5 GiB |
| u26 | 20260731.0.0 | `9dc7c5363c0146a08ba0c9aa834d82c2c6dfbb1c471ad9a2f0aba1189e21be05` | 860,447,744 | 3.5 GiB |

The machine-readable source/input result is retained as
`inputs/input-audit.tsv`, with per-entry info and check output under
`inputs/audit/`.

## Formal UEFI results

Each final run used an empty mode-0700 work directory and a 300-second
readiness timeout. The harness created a 64 GiB root overlay and standalone
64 GiB data disk, pure-Go CIDATA, one native KVM VM, and login user `dba`. It
then verified the guest contract, stopped, restarted, checked the data canary,
stopped again, and removed its short runtime directory.

Paths below are relative to the retained evidence root. Times are UTC.

| Alias | Start–end | Result | Artifact path | Evidence SHA-256 | Final disks |
|---|---|---|---|---|---|
| el8 | 19:33:28–19:33:59 | passed | `artifacts/el8-uefi` | `f120333cdabe7d40ed4c376e46ef7836a00b030dc9a53049d943bce8e7e523c7` | 2/2 passed |
| el9 | 19:33:59–19:34:31 | passed | `artifacts/el9-uefi` | `73045da9e34901eaba83016dece5442f89e2a00867e2d52ee11374c65c0375ff` | 2/2 passed |
| el10 | 19:34:31–19:35:04 | passed | `artifacts/el10-uefi` | `d1bcae3736cfd4a5bbf8f00fbcec0fe04cb781ba74c6ac291b4473dcb69e7793` | 2/2 passed |
| d12 | 19:28:39–19:29:02 | passed | `artifacts/d12` | `0a19399ca5219ce80cf5bb020a12efae74b806a04eca332e505f10119d650e97` | 2/2 passed |
| d13 | 19:29:02–19:29:25 | passed | `artifacts/d13` | `2e77ace81bfd8cfdb33abac12a76a28ab73d7c65d71457586a0c20f73654dff0` | 2/2 passed |
| u22 | 19:29:25–19:29:56 | passed | `artifacts/u22` | `7d20eb66936e6f38d812ad580504f7bb66d1a00a5afbad04dbca19c97163927d` | 2/2 passed |
| u24 | 19:29:56–19:30:31 | passed | `artifacts/u24` | `75e8aa214bafc5d94426aa75db0198a3bb66e3ec4b03fca04f7428c1669bc2a9` | 2/2 passed |
| u26 | 19:30:31–19:33:10 | passed | `artifacts/u26` | `51d274eae312ffd47e03405c87eb73c64fe3c7badc3b2a7d2e7a4ef5cf28e048` | 2/2 passed |

`artifacts/formal-results-final.tsv` is the authoritative result table. All
eight evidence JSON files say `boot=uefi`, name the same OVMF code path, report
QEMU 8.2.2 and Linux/amd64, end with `smoke=passed`, and contain two captured
UID-1000 QEMU identities.

## Cross-family guest contract

All eight final entries proved:

- guest `x86_64`, `dba` SSH, generation/spec-hash ready marker;
- root filesystem grown above 60 GiB;
- `/data` filesystem above 60 GiB, discovered by its deterministic virtio
  by-id serial and persisted by filesystem UUID plus `nofail`;
- DNS lookup and the injected outbound network check returned HTTP 200;
- NTP synchronized;
- `/data/piglet-m0-persist` survived stop/start;
- final stopped state and `runtime-residue=none`.

Rocky 8/9/10 used XFS for `/data`; Debian 12/13 used ext4; Ubuntu 22/24/26
used XFS on these images. Recorded root filesystem sizes ranged from
65,421,303,808 to 67,549,245,440 bytes. Recorded data filesystem sizes ranged
from 67,049,664,512 to 68,685,922,304 bytes.

U26 spent most of its 159-second run in its own
`systemd-networkd-wait-online` startup job, but completed inside the explicit
300-second timeout and passed the full lifecycle. Other successful final runs
took 23–35 seconds.

After all VMs were stopped, every formal root and data qcow2 was inspected by
backing chain and checked again: 16 sections, 16 `No errors were found`
results. Root overlays referenced their exact read-only inputs with explicit
qcow2 backing format; data disks were standalone.

## Preserved BIOS failures and root-cause proof

The initial BIOS run retained three relevant outcomes:

- el8 happened to pass under BIOS; evidence SHA
  `fffa1ba2357611cabba37a102c07b5b83e69fbf9565b3869e7956c7c17cadc77`;
- el9 failed after the full 300 seconds; evidence SHA
  `2890001455f7b5d3725031ef257f78218720a2f5871134ab5e38fa55d6079f10`;
- el10 failed after the full 300 seconds; evidence SHA
  `976df182124b93035e3500edafddf43b3b8b28062b90d819bed9411047aad563`.

Both failures used UID-1000 KVM QEMU but had no pflash/OVMF arguments, produced
zero-byte serial logs, and ended in SSH banner/readiness timeout. The same
el9/el10 bytes and digests passed in 32/33 seconds after explicit UEFI. This is
direct paired evidence that boot mode—not image corruption, KVM, checksum, or
guest cloud-init content—caused the original failures.

The failed harnesses quit their QEMU processes but, because they did not reach
normal success cleanup, left two empty mode-0700 runtime directories. Before
removal, their names were derived from the matching evidence project UUIDs;
owner/mode and an empty allowlisted entry set were verified, and no QEMU was
running. Only those two empty owned directories were removed with exact
`rmdir`. All failure workdirs, serial logs, disks, seeds, keys, stdout/stderr,
and evidence JSON remain retained.

## Audit-script errors and evidence boundary

Two post-run aggregation commands initially failed without changing guest
artifacts:

1. the first formal merge used jq dot syntax for hyphenated check keys;
2. the first final summary used an ANSI-C tab expression that an outer SSH
   shell misquoted.

The empty/partial outputs and notes were retained; corrected commands wrote new
`*-final` files. These were audit-script errors, not guest failures.

The final audit found:

- formal results 8/8 and final runtime-disk checks 16/16;
- no QEMU process, M0 runtime directory, private lease, `.partial`, or quick
  loopback listener;
- formal and failed artifact directories mode 0700;
- every retained private key mode 0600.

The evidence root contains base images, overlays, data disks, seeds, and
generated private keys and must be treated as sensitive. Nothing was pruned or
destroyed. `artifacts/EVIDENCE_SHA256SUMS` covers 141 retained non-disk evidence
and log files and has SHA-256:

```text
f4c5259ef098aac7113ae236cff2c62136db652cc918555febcefc305bf7e2f0
```

This closes one native Linux/amd64 smoke for each formal guest alias. It does
not by itself mark image manifest entries `supported`, prove macOS/arm64,
private networking, Pigsty workloads, release signing, or GA readiness.

