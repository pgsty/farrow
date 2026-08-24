# Current MinIO profile product lifecycle — Darwin arm64 — 2026-08-24

Result class: **native real product E2E** on the Darwin/arm64 Tier-1 host. The
current checked-in `profiles/minio.yaml`, normalized to login user `dba`, passed
four-node create/boot, fixed-IP access, control-only lateral SSH, sixteen data
disk contracts, stop/start persistence, twenty stopped-disk checks, and scoped
destroy.

This validates the VM/network/storage contract. It does not install or test the
MinIO application.

## Identity and retained evidence

- Host: `m1.vonng-rognet`, Darwin arm64, QEMU/qemu-img 11.1.0 with HVF.
- Evidence window: `2026-08-24T05:44:57Z` through
  `2026-08-24T05:47:45Z`.
- Retained mode-0700 root:
  `/Users/vonng/Library/Caches/piglet/minio-product-darwin-dba-go127-20260824-01`.
- Project: `a2d4760b-375e-4756-af35-032dbf735e23`.
- Resolved spec hash:
  `acacc823a2914e6d351b9e384aeb78c803ca8063507baf3ef5f809e606fce060`.
- Go 1.27.0 Piglet binary SHA-256:
  `5d71dc175e97336810569d8019b5b9e53dd8bbdbca0b9c456aa570c5da9d32d6`.
- Profile SHA-256:
  `7f27b525d9d7cabd889bfe633187f8433ce39d56be24fc8c8f75f6f6e29d49d6`.
- Ubuntu Server 24.04 arm64 base SHA-256:
  `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476`.

The source image was imported through the public command into the isolated
digest cache. The managed base remained a mode-0444, backing-free qcow2 and its
digest was rechecked after destroy.

`artifacts/EVIDENCE_SHA256SUMS` covers the retained evidence and has SHA-256:

```text
1bda3c6fecdbdf0cc9fe71622975b907078d1db4fbe04415762b72bbd85fd233
```

## Preflight and exact topology

The rebuilt typed preflight enumerated the complete Darwin IPv4 routing table,
verified the pinned public socket_vmnet projection, exact host `.1/24` address
and `/24` route, and returned:

```text
installation=protected mode=host interface=bridge100
cidr=10.10.10.0/24 ready=true exit_code=0 findings=[]
lease.active=false
```

`protected` is intentional: the ordinary user can verify the pinned public
files, digests, socket, interface, and route, while the mode-0600 state below a
mode-0700 root directory remains unreadable without privilege.

Read-only plan returned non-destructive `create`. Public `up` reached:

```text
minio-1  10.10.10.10  control  dba  1 CPU / 2 GiB  NAT SSH 2222
minio-2  10.10.10.11           dba  1 CPU / 2 GiB  NAT SSH 2223
minio-3  10.10.10.12           dba  1 CPU / 2 GiB  NAT SSH 2224
minio-4  10.10.10.13           dba  1 CPU / 2 GiB  NAT SSH 2225
```

Every node had a 64 GiB root and four independent 32 GiB data disks mounted at
`/data1` through `/data4`. The clean machine table is
`artifacts/guest-contract-clean.tsv`.

## Network and SSH contract

For all four nodes, product exec proved `dba`, native `aarch64`, one vCPU,
approximately 2 GiB RAM, the exact private address on `private0`, and the only
default route on `mgmt0`. Host ICMP and direct host SSH as `dba` passed for
`.10` through `.13`.

The control guest owned `/home/dba/.ssh/id_ed25519` as `dba:dba` mode 0600 and
SSHed to all three workers by node name. Each worker proved that the control
private key was absent.

## Sixteen data disks and persistence

For every disk, the run resolved `/dev/disk/by-id/virtio-<20-char-serial>`,
checked the exact 34,359,738,368-byte block size, matched the mounted block
device, verified XFS UUID/fstab `nofail`, and wrote a unique canary. Results:

- 16/16 serial/by-id/device/size/mount/filesystem/UUID/fstab checks passed;
- 16/16 canary SHA-256 values matched after public `stop` then `start`;
- four new running PIDs appeared after restart;
- the lease was released while stopped and reacquired only for start.

The complete tables are `artifacts/disk-contract.tsv` and
`artifacts/canaries-after-restart.tsv`, with SHA-256 respectively:

```text
d382f1bf0af1f25990897ffae0680a1074b6b255e39ee012af1d082b08388005
d25415f25d67c4f247e8d122f238334d394be64f2513fe5986b10a25997e068b
```

## Stopped checks and destroy

After the final stop, all 20 runtime qcow2 files passed `qemu-img info
--backing-chain` and `qemu-img check -f qcow2`:

- four 64 GiB root overlays with the verified immutable base;
- sixteen standalone 32 GiB data disks;
- 20/20 `No errors were found` results.

The check log SHA-256 is
`1590d7980a4707a7d321666ed1a0e55429441c40526b3d62c4692c4ea4896d93`.

Public `destroy --force --json` returned all four nodes absent and removed only
their node artifacts. It preserved the isolated image cache and the project
Ed25519 private/public key pair byte-for-byte. `known_hosts` became empty as
designed because its destroyed-node entries were removed. Final network status
was healthy/protected with no finding and an inactive lease. No QEMU process
referenced this project root. One unrelated user-NAT QEMU process in a separate
`/private/tmp/piglet-perf2-*` project appeared concurrently and was not touched.

## Audit correction and boundary

The retained run completed the product lifecycle, then its first final audit
incorrectly required the entire keys directory to remain byte-identical. That
failed only because correct destroy cleanup changed `known_hosts`. The bounded
finalizer proved that only `known_hosts` changed, both Ed25519 key files stayed
identical, all 16 restart hashes matched, all 20 disk checks passed, destroy
completed, and the global lease was inactive. This correction is retained in
`artifacts/audit-correction.txt`.

Earlier discarded orchestration attempts exposed argument-order, guest
read-permission, and TSV-column mistakes before this retained clean run. They
did not change product code or the global network; every attempt that had
started VMs was safely stopped and scoped-destroyed before its evidence root
was removed.

This evidence closes current-profile Darwin arm64 MinIO VM semantics. It does
not prove Linux amd64 with the new `dba` identity, a MinIO server workload,
host reboot persistence, or production package installation.
