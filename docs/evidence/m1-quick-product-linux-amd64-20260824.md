# M1 product quick lifecycle — Linux amd64 — 2026-08-24

Result class: **native real product E2E** on the Tier 1 Linux host. The public
quick path passed a fresh no-state/no-YAML/no-flags boot, SSH/exec and outbound
networking, native KVM/non-root process checks, real business-port forwarding,
disk sizing and persistence, ten stop/start cycles, guarded destroy, and final
no-residue checks. Two product observations and one audit-script error are
recorded explicitly below.

## Runner and execution identity

- Host: `vonng-aimax`, Ubuntu 24.04, Linux
  `6.17.0-35-generic`, x86_64.
- Invoking account: UID/GID 1000 `vonng`, member of `kvm`; `/dev/kvm` was
  root:kvm 0660 and readable/writable.
- QEMU/qemu-img: Debian Ubuntu package 8.2.2.
- Accelerator/machine/CPU: `kvm`, `q35`, `host`; firmware
  `/usr/share/OVMF/OVMF_CODE_4M.fd`.
- Evidence window: `2026-08-23T17:52:51Z` through
  `2026-08-23T18:01:10Z` (`2026-08-24 01:52–02:01` Asia/Shanghai).
- Isolated retained root:
  `/data/piglet-v1-product-quick-20260824-irw1cU`, mode 0700,
  owner `vonng:vonng`.

The requested local `bin/piglet` was first transferred and retained verbatim as
`inputs/piglet-darwin-arm64-input`. Its SHA-256 is
`33c1de01361e4a5cafc546fd5dd8661bf3b83a94773b92cd5f4cb757b876093a`,
but `file` correctly identifies it as Mach-O arm64, so it cannot execute on the
Linux runner. The same current source was cross-built outside the repository
with Go 1.26.7, `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath`, then
transferred as `inputs/piglet`. The executed static ELF SHA-256 is:

```text
bff10c22d4d49ac9d2bf9cc0866d4fb7c0df55a2bb08d51d40c55a254600bdcd
```

`go version -m` records the expected pinned dependency versions and
`vcs.modified=true`. This repository still has no commit, so this evidence is
bound to the exact ELF digest rather than an invented source revision. The
remote host did not have Go installed; it only executed the transferred binary.

The `/data` root is root-owned 0755. `sudo -n` was used once to create the new
unique evidence root and immediately chown it to `vonng`; no Piglet quick
command used host sudo. No existing project was mutated, and
`/data/pgsty/piglet` was not used as an input or modified; cache inventory
excluded its results.

## Existing-image import and primary product run

The pre-existing source image was used read-only:

```text
/data/piglet-v1-e2e-20260823-2330/data/cache/images/sha256/
  0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe.qcow2
```

It was mode 0444, 624,239,616 artifact bytes, plain backing-free qcow2 with a
3,758,096,384-byte virtual size. Its filename digest matched a fresh full-file
SHA-256, and `qemu-img check -f qcow2` reported no errors. The public named
import command copied it into the new managed cache, published the destination
mode 0444, and registered immutable amd64/UEFI/bootstrap-user metadata:

```bash
PIGLET_DATA_HOME="$R/data" "$P" image import --json \
  --sha256 0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe \
  --name local-u24 --boot uefi --source-user ubuntu "$SOURCE"
```

The destination and old source remained byte-identical. With an empty `work/`,
no `piglet.yaml`, and no project marker, this command created the primary VM:

```bash
PIGLET_DATA_HOME="$R/data" "$P" up --image local-u24 --json
```

It completed in 11 seconds with exit 0. Project
`ee71a00f-3f82-4a61-8dd1-35ba00357698` reached `running`; a subsequent literal
`piglet up --json` with no YAML or flags returned exit 0 and `already running`.

## Strict fresh bare-default run

To test the exact first-run promise independently of the explicit local-image
selection, a second empty project used new `bare-default/work` and
`bare-default/data` directories. Preflight recorded no YAML, marker, or workdir
entry. The following command had no flags:

```bash
PIGLET_DATA_HOME="$B/data" "$P" up --json
```

It downloaded the embedded amd64 u24 artifact, verified digest
`b3064efb500d71d6ccbe619b1716062b803e285116e040627b430aaee14cced6`,
published it mode 0444, and created project
`5d25aaa1-12c2-491c-9ab6-5d29346a6a42` in 27 seconds. The result was
`running`, image alias `u24`, release `20260801`, and the five expected SSH and
business forwards. One stop/start preserved a guest `/data` canary, and guarded
destroy plus the final cache `qemu-img check` passed.

## Runtime, guest, and forwarding assertions

For both projects, the identified QEMU process was UID/GID 1000 with PPID 1
after `-daemonize`. The argv contained:

```text
-machine q35 -accel kvm -cpu host -smp 2 -m 4096
-netdev user,id=mgmt,
  hostfwd=tcp:127.0.0.1:2222-:22,
  hostfwd=tcp:127.0.0.1:15432-:5432,
  hostfwd=tcp:127.0.0.1:13000-:3000,
  hostfwd=tcp:127.0.0.1:18080-:80,
  hostfwd=tcp:127.0.0.1:18443-:443
```

There was no Lima, Vagrant, libvirt, `virtqemud`, bridge-helper, or child-helper
process in the runtime chain. All five loopback listeners belonged to that
same non-root QEMU PID.

Public `piglet exec` proved SSH as `dba` UID 1000, guest `x86_64`, 2 online
CPUs, approximately 4 GiB memory, and outbound HTTPS status 200. The primary
guest reported:

- root block device: 68,719,476,736 bytes; root filesystem
  65,445,814,272 bytes after growth;
- data block device: 68,719,476,736 bytes, mounted at `/data`;
- data fstab entry by filesystem UUID with `nofail`;
- generation-1 ready marker with the exact project/spec hash.

Four temporary guest TCP responders then made the business mappings observable
end to end rather than merely present in argv. Host connections returned:

```text
15432->5432 response=piglet-forward-5432
13000->3000 response=piglet-forward-3000
18080->80   response=piglet-forward-80
18443->443  response=piglet-forward-443
```

## Ten-cycle lifecycle and destroy boundary

The primary project completed ten sequential public `stop` → `start` cycles.
Every cycle returned exit 0 and asserted:

- the old PID was dead;
- the prior runtime directory and all five listeners were absent while stopped;
- the next QEMU process was UID 1000 and used `-accel kvm`;
- materialized ports remained exactly
  `2222,15432,13000,18080,18443`;
- `/data/piglet-product-quick-canary` retained SHA-256
  `8bfba340f93110d58407d67fbc1c71e4c055ea495d2ab111a0b05117d7ae151a`.

`artifacts/cycles.tsv` contains all ten results. The VM was then destroyed from
the running state with:

```bash
PIGLET_DATA_HOME="$R/data" "$P" destroy --force --json
```

Destroy returned exit 0 and structured state `absent`. Pre/post inventories
proved that it removed the node directory, root/data/seed/NVRAM/state/logs, the
QEMU process, runtime directory, and listeners, while preserving the work and
data-root project markers, resolved spec, managed image cache, project lock,
and project Ed25519 key pair byte-for-byte. `known_hosts` changed from one node
endpoint to an empty mode-0600 file because destroy removes that endpoint; the
private/public project key hashes did not change. An unrelated evidence canary
also remained intact.

The exact same cache/key-pair preservation and node/runtime cleanup checks
passed for the bare-default project. Both retained cache images passed a final
stopped-state `qemu-img check`.

## Failures, observations, and evidence boundary

1. The first guest contract collection exited 1 because the audit command gave
   one `findmnt` invocation two target paths under `set -e`. The product VM
   stayed running. The original failure files were retained; the corrected
   command split the targets and passed CPU/memory/disk/fstab/ready/outbound/
   canary checks with exit 0. This was an audit-script error, not a Piglet
   lifecycle failure.
2. `piglet doctor --json` returned capability exit 3 because
   `systemd-networkd` was inactive. Every quick-relevant host, QEMU, KVM,
   qemu-img, SSH, project, and data-root check was `ok`; only the unused private
   network setup was `error` plus a bridge-helper warning. This is preserved as
   a product UX observation.
3. After successful destroy, `piglet status --json` returned exit 1 with a
   missing node `state.json` error instead of a structured `absent` status.
   Destroy itself had already returned structured `absent` with exit 0, and all
   independent cleanup checks passed. The post-destroy status behavior remains
   a product gap; it is not hidden by this E2E result.
4. The ten-cycle gate used the explicitly imported `local-u24` image. The
   independent strict bare-default project used the embedded u24 image and one
   stop/start cycle. No claim is made that the embedded-image project ran ten
   cycles.

The final root-level audit found no QEMU process, loopback listener, private
lease, project runtime directory, node directory, or `.partial` file for either
project. It did not remove the retained project markers, keys, caches, inputs,
or logs. The evidence root was 874 MiB at lifecycle close and remains mode 0700
because it contains generated project private keys and must be treated as
sensitive. The later metadata test artifacts increased it to 878 MiB.

Replay artifacts include:

- `artifacts/up-initial.json`, `up-bare-noop.json`, `cycles.tsv`,
  `destroy.json`, and the pre/post destroy inventories;
- `logs/runtime-running-retry.txt`, `guest-contract-retry.txt`, and
  `forward-e2e.txt`;
- `bare-default/artifacts/up-bare.json`, lifecycle JSON, destroy JSON, and
  post-destroy audit;
- `artifacts/final-audit.txt` and `final-summary.txt`;
- `artifacts/SHA256SUMS`, covering the original 202 retained lifecycle
  input/log/artifact files, with SHA-256
  `ae10792d486801096298b491e3ebd8c59ec9fa193cd0a96b9d7caf6353ca7ac4`.

## Linux host metadata and xattr follow-up

A later native metadata check compiled only the current
`internal/hostconfig` package tests for Linux amd64. Two source snapshots taken
around the build were byte-identical. The compile command was:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go test -c -trimpath -o hostconfig-linux-amd64.test \
  ./internal/hostconfig
```

The resulting Go 1.26.7 static ELF was transferred to the existing evidence
root as:

```text
inputs/hostconfig-linux-amd64-9a4409af6547.test
SHA-256 9a4409af65478dae00a9347d2a8ae7ea0744a56c90415bdfc40061a0a02ac72b
```

The binary is mode 0555 and its adjacent `.sha256`, `.output.txt`,
`.metadata.txt`, and `.exit` records are mode 0444. It ran as ordinary UID/GID
1000 `vonng`, without sudo, using `inputs/` as `TMPDIR` so the real temporary
target/staging files lived on the evidence filesystem and were removed by
`testing.T.TempDir`:

```bash
TMPDIR="$INPUTS" "$TEST_BINARY" -test.v -test.count=1 \
  -test.run 'TestApplyHelperIsDigestBoundAtomicAndPreservesMetadata|TestApplyHelperRefusesStaleTargetAndSymlink|TestValidateTransition'
```

Native environment facts were:

- kernel `6.17.0-35-generic`, Linux x86_64;
- `/data` backed by `/dev/nvme1n1p3`, XFS, mounted
  `rw,relatime,attr2,inode64,logbufs=8,logbsize=32k,sunit=8,swidth=8,noquota`;
- 4096-byte XFS blocks/fundamental blocks;
- `getenforce` and `sestatus` absent and no
  `/sys/fs/selinux/enforce`, so SELinux was not enabled on this runner;
- no directory existed under `inputs/` before or after the test run.

All four selected tests ran and passed with exit 0 and zero skips:

```text
PASS TestApplyHelperIsDigestBoundAtomicAndPreservesMetadata
PASS TestApplyHelperRefusesStaleTargetAndSymlink
PASS TestValidateTransitionRejectsChangesOutsideOwnedBlock
PASS TestValidateTransitionRechecksCrossProjectNameConflicts
```

The metadata test was genuinely executed, not skipped. On this XFS filesystem
it successfully set `user.piglet-test`, performed the digest-bound atomic
replacement, then read back the exact `preserve-me` value while also verifying
mode and Linux inode flags. The stale-digest and symlink target refusals, and
both unowned-byte/cross-project transition checks, also passed. Because SELinux
was disabled, this proves native Linux XFS user-xattr and inode-flag
preservation but does **not** constitute an SELinux-label preservation test.
