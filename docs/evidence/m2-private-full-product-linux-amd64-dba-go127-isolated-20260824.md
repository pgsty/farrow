# Current `full` profile — Linux amd64 native KVM isolated host namespace — 2026-08-24

Result class: **native KVM product E2E in the isolated Ubuntu systemd host
namespace described by the current MinIO evidence**. The current all-`dba`
`profiles/full.yaml` passed network install, four-node boot and fixed-IP access,
four 128 GiB data disks, stop/start persistence, stopped qcow2 checks, scoped
destroy, and full network uninstall/restore.

## Identity

- Evidence window: `2026-08-24T06:19:41Z`–`06:20:44Z`.
- Retained mode-0700 root:
  `/data/piglet-v1-current-linux-container-20260824.bbwHvS/evidence/full-current`.
- Go 1.27.0 linux/amd64 Piglet:
  `3fb2601c37fe6b55a2d83c434b70cd2169a79c2b167925ebd0f0027c853d6124`.
- Profile:
  `454769bb0883a775e9360aeac822653efca72b39754a500a7d6483f92063a601`.
- u24 base:
  `0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe`.
- Project `e83b70aa-899b-47d2-a934-79272f6a059c`, spec hash
  `d7bbf904a9218856acc6f984d2982236e8be243e2726f017848a18295e59c0ab`.
- Evidence checksum-list SHA-256:
  `56ee86d51a1d400b11ac6ddc4f846eb7628681faa83f0014e378a70be072465c`.

## Result

Current exact topology reached:

```text
meta    10.10.10.10  control  dba  2 CPU / 4 GiB  NAT SSH 2222
node-1  10.10.10.11           dba  1 CPU / 2 GiB  NAT SSH 2223
node-2  10.10.10.12           dba  1 CPU / 2 GiB  NAT SSH 2224
node-3  10.10.10.13           dba  1 CPU / 2 GiB  NAT SSH 2225
```

All nodes proved native x86_64/KVM, exact private address, management-only
default route, outbound HTTPS, container-host ICMP/direct SSH, and no control
key on workers. `meta` owned its `dba:dba` mode-0600 key and reached all peers
by name.

Every node had a 64 GiB root plus one exact 137,438,953,472-byte XFS `/data`
disk with by-id serial, UUID fstab `nofail`, and canary. Four canaries matched
after stop/start. Final stop produced eight clean qcow2 results: four roots and
four 128 GiB data disks. Public destroy returned four absent nodes and
preserved the keypair and immutable image cache.

```text
disk-contract.tsv           29c1b0b460c1a481648540ed9030d11200b777be8842cecfd8a23c2d78926ea0
canaries-after-restart.tsv  b0ed255ee08b54e292bd576ae4107b435a7a19e4aa8369b96886b79299311321
qemu-img-check.log          558a8da6c874aca023248b4af41f23163f23fb17c0362f167fc44e306932e45a
```

Network uninstall restored helper root:root 0755, removed its dpkg override,
deleted every Piglet bridge/network/manifest/lease path, and restored networkd
prestate. Neither the container nor the physical `ai` host retained
`piglet0`/`10.10.10.0/24`; existing host services were untouched.

This refreshes the exact `full` login identity and storage/network semantics on
Linux current code. It does not replace the earlier bare-host 30-cycle/crash
evidence or prove a bare-host current-identity reinstall.
