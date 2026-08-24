# Product private lifecycle — Linux amd64 — 2026-08-24

Result class: native real product E2E on the Tier 1 Linux host, followed by a
second verified host-network uninstall/no-residue restoration.

## Execution identity

- Host: `vonng-aimax`, bare-metal Ubuntu 24.04.3 LTS, kernel
  `6.17.0-35-generic`, x86_64, KVM, QEMU 8.2.2.
- Repository: unborn `main`; isolated source snapshot at
  `/data/piglet-v1-e2e-20260823-2330/src`.
- Product binary SHA-256:
  `79d46ba93ae54d3af220813e5a58f9e71b79561c6a0e389009235ee95a5944c8`.
- Product workspace:
  `/data/piglet-v1-e2e-20260823-2330/product-private-linux-work`.
- Product data root:
  `/data/piglet-v1-e2e-20260823-2330/product-private-linux-data`.
- Project: `dddae1cd-30d4-4fb6-b1da-489d0d99a6d1`.
- Spec hash:
  `c52e8abaef9a7b3ccffcc572275b89daa43a56267bf90f0595fe62ac1ff2591b`.

The amd64 u24 qcow2 was imported as immutable named alias `local-u24`, digest
`0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe`,
with boot contract `uefi`. Alias registry SHA-256:
`882207ccf2c0e5c3504f538da4af8352786b7eb898898e5a7712fb4b4f83f199`.

The host-network plan was regenerated from the restored clean baseline:
`/data/piglet-v1-e2e-20260823-2330/artifacts/linux-net-stage-product/install-plan.json`,
SHA-256
`6c0c1cb34dd531138b8b1698f1418245d216164b3c6dcbb22ee26a6876202cb9`.
It used the same allowlist, original disabled/inactive four-unit snapshot, and
reversible root:kvm 4750 dpkg override as M0-C.

## Fail-closed retries

The first public `up` attempt discovered that a precreated root-owned mode-0666
lease lock can be opened/flocked by the user but cannot be chmodded by that
user. The old code performed an unnecessary unconditional chmod and stopped
before acquiring a lease or creating QEMU. The fix validates an existing
root/current-UID mode-0666 inode without changing it; only a newly created
same-UID lock is chmodded. Fstat revalidates the opened inode, and concurrent
creation tests cover the short umask window.

The retry then found the safe pre-state project marker/key directory left by
that failure. Dispatch and manager logic were changed to re-enter an explicit
private `up -f` when `resolved.json` is not yet present. No artifact was
deleted, and the same project ID continued.

## Product lifecycle result

The public sequence was:

```text
piglet plan -f tests/e2e/private-two-local.yaml --json
piglet up -f tests/e2e/private-two-local.yaml --json
piglet status --json
piglet up -f tests/e2e/private-two-local.yaml --json
piglet stop --json
piglet start --json
piglet stop --json
piglet status --json
```

The two VMs used UEFI according to the image entry, with per-node copied vars:

```text
/usr/share/OVMF/OVMF_CODE_4M.fd  # read-only code
/usr/share/OVMF/OVMF_VARS_4M.fd  # copied to each node nvram.fd
```

| Node | Private IP | Initial PID | VM UUID |
|---|---|---:|---|
| meta | 10.10.10.10 | 104709 | `9e3d5a4a-eb18-4dca-80b6-f816e5008347` |
| node-1 | 10.10.10.11 | 104889 | `6f610eb8-89d5-46fa-a6f2-392f18512b5a` |

Assertions passed:

- QEMU UID 1000, KVM, OVMF, management user NAT, helper bridge backend;
- two tap members while running, none after each stop;
- exact private address/interface UP/no-default/no-DNS on both nodes;
- host→VM, VM→VM, both management internet HTTP 200;
- control-only key, lateral SSH, worker key absence;
- idempotent second `up` retained PIDs/identity;
- stop released lease and removed runtime/taps while preserving disks/keys;
- start reacquired the same project/node reservation and VM UUIDs;
- `/data/piglet-product-canary` survived stop→start;
- final public status reported both nodes stopped, with no QEMU/tap/lease.

Private `destroy --force` returned capability exit 3 and did not change the
resolved-state hash or disk paths. While the first project was running, a
second workspace `up` returned typed lease conflict exit 6. It created no
resolved/node/disk state; the first project remained running and stopped
normally afterward.

Final retained state hashes:

- resolved: `3c05ac54d3b2ecee370adc647c7b35527b25d76f81a118fc23bbd71da5dd3453`;
- meta: `2c0f99aaf768f35d6f70d811c24ae37d3a61001a7eff7a1598f341293f7d7159`;
- node-1: `3f169ef14ce6cac435564de09ffd5bd283cbb0b511b57427dff95b0f5ec4d332`.

## Final host restoration

After final stop, uninstall again required no lease, no QEMU, no bridge member,
exact file hashes, and matching helper override. It deleted only the owned
allowlist, restored helper root:root 0755 and all four networkd units to
disabled/inactive, and removed state/lock/directories only when empty.

Postconditions passed: no `piglet0`, route, Piglet host config, override,
lease/state/lock, `/etc/qemu`, `/var/lib/piglet`, or `/run/piglet`; NetworkManager
remained active. Product workspaces, qcow2, seed, state, and key evidence under
the isolated `/data/piglet-v1-e2e-*` tree were retained.

## Remaining boundary

Both Tier 1 hosts now pass public private plan/up/idempotent-up/status/stop/
start, real lease, guest contract, and data persistence. Remaining M2 work is
the public network executor, private SSH/exec/logs/debug/repair/destroy/
recreate semantics, typed drift, partial/crash/reboot recovery, shared/FD
fallback composition, and 30-cycle soak.
