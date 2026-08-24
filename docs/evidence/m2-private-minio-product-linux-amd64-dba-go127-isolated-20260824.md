# Current MinIO profile — Linux amd64 native KVM isolated host namespace — 2026-08-24

Result class: **native KVM product E2E in an isolated systemd/network
namespace on the Linux amd64 Tier-1 physical host**. The current all-`dba`
`profiles/minio.yaml` passed container-local network install, four-node boot,
fixed-IP and lateral access, sixteen data-disk contracts, stop/start
persistence, twenty stopped qcow2 checks, scoped destroy, and network
uninstall/restore.

The isolation boundary matters: this is stronger than a mock or nested TCG
run, but it is not a second bare-host install. The physical host supplied its
real `/dev/kvm`; systemd, `piglet0`, helper mutation, and all QEMU processes
were confined to a dedicated Docker network/root namespace. The physical host
never received a Piglet route, interface, or network file.

## Runner and identity

- Physical host: `vonng-aimax`, Ubuntu 24.04, Linux 6.17, amd64, nested KVM
  enabled and `/dev/kvm` accessible to the invoking user.
- Isolated host: Ubuntu 24.04 systemd container `piglet-e2e-current-20260824`,
  image digest
  `sha256:0b1797bfa13abc6eb10d93003cc572d1b021921b0757c445526d6b4d249515be`.
- Container network before install: only Docker `172.17.0.0/16`, default via
  `172.17.0.1`; no host networking mode was used.
- Invoking container account: UID 1000 `ubuntu`, supplementary numeric host KVM
  group 993; QEMU ran as this account, not root.
- QEMU/qemu-img: Ubuntu QEMU 8.2.2, KVM, q35/host CPU, OVMF.
- Evidence window: `2026-08-24T06:11:44Z` through
  `2026-08-24T06:15:45Z`.
- Retained mode-0700 root:
  `/data/piglet-v1-current-linux-container-20260824.bbwHvS/evidence/minio-current`.

Exact identities:

```text
Piglet Go 1.27.0, linux/amd64, CGO_ENABLED=0
Piglet SHA-256  3fb2601c37fe6b55a2d83c434b70cd2169a79c2b167925ebd0f0027c853d6124
profile SHA-256 7f27b525d9d7cabd889bfe633187f8433ce39d56be24fc8c8f75f6f6e29d49d6
u24 SHA-256     0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe
project          ce467af1-95c8-470c-9570-c276efe3ca2a
spec hash        accc823a2914e6d351b9e384aeb78c803ca8063507baf3ef5f809e606fce060
```

The retained `artifacts/EVIDENCE_SHA256SUMS` has SHA-256:

```text
de42ba24abc6287417211c06f1d053b0ece976b9a0acccae182e2c6050ea86a1
```

## Clean preflight and network lifecycle

Initial typed preflight returned clean eligibility with
`installation.status=absent`, one warning, `ready=true`, and exit 0. The first
dry-run exposed a real compatibility defect: `NetworkManager.service` was
absent on the minimal host namespace, but discovery had required its empty
`UnitFileState`. The implementation now treats an exact systemd `not-found /
inactive / dead` NetworkManager state as optional while retaining strict
networkd requirements. Unit/race/vet coverage was added before the tested
binary was rebuilt.

Public dry-run and apply then passed. The exact plan and applied checks proved:

- `piglet0` at `10.10.10.1/24`;
- root:kvm 4750 reversible dpkg-statoverride from the original package-owned
  root:root 0755 helper;
- no NetworkManager drop-in because NetworkManager was absent;
- non-root QEMU/helper attach QMP smoke passed;
- root-owned manifest, networkd files, tmpfiles/lease boundary, and inactive
  lease were exact/protected.

## Current all-`dba` four-node contract

Read-only plan was non-destructive `create`. Public up reached:

```text
minio-1  10.10.10.10  control  dba  1 CPU / 2 GiB  NAT SSH 2222
minio-2  10.10.10.11           dba  1 CPU / 2 GiB  NAT SSH 2223
minio-3  10.10.10.12           dba  1 CPU / 2 GiB  NAT SSH 2224
minio-4  10.10.10.13           dba  1 CPU / 2 GiB  NAT SSH 2225
```

For every node, product exec proved native x86_64/KVM, `dba`, one vCPU,
approximately 2 GiB RAM, exact private address on `private0`, no private default
route, management default route, and outbound HTTPS. The isolated host
namespace reached every fixed IP by ICMP and direct SSH as `dba`.

The control node's key was `dba:dba` mode 0600 and reached all workers by name.
All workers proved that the lateral private key was absent.

## Sixteen disks, restart, and stopped checks

Every node had a 64 GiB root plus four independent 32 GiB XFS data disks at
`/data1..4`. For each data disk the run verified its deterministic 20-character
serial, `/dev/disk/by-id` target, exact 34,359,738,368-byte size, mounted block
device, filesystem UUID, fstab `nofail`, and a unique canary.

- 16/16 initial disk contracts passed.
- Public stop/start completed for all four nodes.
- 16/16 canary SHA-256 values matched after restart.
- Final stop left no project QEMU process.
- Four root overlays and sixteen data disks, 20/20 total, passed backing-chain
  inspection, exact virtual-size checks, and `qemu-img check`.

Machine tables/log hashes:

```text
disk-contract.tsv            90506f5dabda912c63e858d67c415816dee6a761d5a101cfd9aee00b587e82d9
canaries-after-restart.tsv   db5cc86de893c4bb58fadaf050054270cf8aa5f0cb10ec17440ae7fb024d022f
qemu-img-check.log           16d5874d2aa14b407b964fcd2f3058851d468cbd00baa54b6307a539d7bc0648
```

## Scoped destroy and complete network restore

Public destroy returned all nodes absent, removed runtime disks/state, and
preserved the isolated immutable image cache plus Ed25519 key pair byte-for-byte.
Public network uninstall then:

- deleted `piglet0` before owned files;
- removed the exact dpkg override and restored helper root:root 0755;
- removed Piglet networkd, tmpfiles, bridge.conf, manifest, state and lease
  paths;
- restored all four networkd unit states;
- left no active lease or project QEMU.

Final typed network status truthfully returned `installation.absent` as a
warning with `ready=true` and exit 0. The initial audit expected the older
clean-host exit 3 and therefore stopped after all product cleanup had already
completed. A bounded finalizer verified the new JSON contract and every restore
condition; the correction is retained in `artifacts/audit-correction.txt`.

Read-only checks on the physical host before and after found no
`10.10.10.0/24` route and no Piglet networkd/manifest path. Existing physical
services and networking were not modified.

## Evidence boundary

This closes current Go 1.27 / all-`dba` Linux amd64 MinIO VM semantics and a
clean Ubuntu-without-NetworkManager install/uninstall path using real KVM. The
earlier bare-host evidence remains the authority for physical-host installer
behavior. This run does not prove host reboot, RPM-family networking, MinIO
application correctness, or production package installation.
