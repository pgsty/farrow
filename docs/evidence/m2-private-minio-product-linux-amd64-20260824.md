# M2 private MinIO product lifecycle — Linux amd64 — 2026-08-24

Result class: **native real product E2E** on the Linux amd64 Tier-1 host. The
exact checked-in `profiles/minio.yaml` completed public network installation,
four-node create/boot, fixed-IP and dual-NIC validation, sixteen independent
32 GiB data-disk contracts, host/NAT/lateral SSH, stop/start persistence,
twenty-disk stopped checks, scoped destroy, and public network uninstall with
clean-host restoration.

This validates the MinIO VM topology and storage contract. It does not install
or exercise a MinIO server/application workload.

## Runner, build identity, and retained root

- Host: `vonng-aimax`, Ubuntu 24.04, kernel
  `6.17.0-35-generic`, x86_64.
- Invoking account: UID/GID 1000 `vonng`, member of `kvm`; `/dev/kvm`
  readable/writable.
- QEMU/qemu-img: Ubuntu QEMU 8.2.2, `q35`, KVM, CPU `host`, OVMF
  `/usr/share/OVMF/OVMF_CODE_4M.fd`.
- Evidence window: `2026-08-23T20:11:05Z` through
  `2026-08-23T20:17:34Z` (`2026-08-24 04:11–04:17` Asia/Shanghai).
- Retained root:
  `/data/piglet-v1-minio-product-linux-20260824-qLHFIF`, mode 0700,
  owner `vonng:vonng`, final size 613 MiB.

Two byte-identical source snapshots bracketed the Linux cross-build. Go 1.26.7
produced static, trimmed binaries. The hosts helper was built first and its
digest injected into the CLI, although this run did not install or invoke the
hosts-file helper:

```text
piglet
5bf04f8235778a44b370ad92e10f57a77eb398f90af4c623b645b1513409eeba

piglet-hosts-helper
846541fa08fb39039d4e09322601490a865bb55a590cfeb866e551d6cea85baf

profiles/minio.yaml
6a7b5334911e3acd55fa3f76cfacd5e67c5ff94713c9f19c6b80bd8b61014bae
```

The repository has no commit, so this evidence is bound to exact binary and
profile hashes rather than an invented source revision. The `/data` parent is
root-owned 0755; sudo was used only to create/chown the new evidence root and
inside the reviewed public network installer. QEMU and ordinary lifecycle
operations always ran as UID 1000. No existing project, unrelated Docker/build
data, or `/data/pgsty/piglet` content was modified.

## Image and clean network prestate

The exact u24 standard amd64 base was reused read-only from an existing
mode-0444 digest cache and imported through the public product command into the
new data root:

```text
SHA-256: 0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe
artifact bytes: 624,239,616
virtual size: 3,758,096,384
format: plain backing-free qcow2
```

Fresh SHA-256, `qemu-img info -f qcow2`, and `qemu-img check` all passed. The
old source remained mode 0444 and unchanged; the new managed copy was also
mode 0444.

Network preflight found no QEMU, `piglet0`, private route, active lease,
Piglet network config/state/runtime, or helper override. All four networkd
units were disabled/inactive, NetworkManager enabled/active, and the distro
bridge helper root:root 0755. Public pre-install status truthfully returned
capability exit 3.

Public network install dry-run and apply both returned exit 0. Applied checks
proved:

- `piglet0=10.10.10.1/24`;
- root:kvm 4750 reversible `qemu-bridge-helper` override;
- NetworkManager remained active and reported `piglet0` unmanaged;
- networkd became active/enabled only after the non-root helper-attach smoke;
- the attach smoke left no QEMU or tap behind;
- the lease root was available and inactive.

## Exact MinIO resolved specification

Strict validation and read-only plan produced spec hash
`844ed20cdf693287ccf7ef8aa5fe6c0b5538b26908415112f9a2139a3e0ff1c1`
and non-destructive `create`:

```text
minio-1  10.10.10.10  control  1 CPU / 2 GiB  NAT SSH 2222
minio-2  10.10.10.11           1 CPU / 2 GiB  NAT SSH 2223
minio-3  10.10.10.12           1 CPU / 2 GiB  NAT SSH 2224
minio-4  10.10.10.13           1 CPU / 2 GiB  NAT SSH 2225
```

Every node resolved a 64 GiB root disk and exactly:

```text
data1  32 GiB  /data1
data2  32 GiB  /data2
data3  32 GiB  /data3
data4  32 GiB  /data4
```

The exact product `up -f inputs/minio.yaml` completed in 18 seconds. Project
`abbbc9e4-50d5-45a9-bf22-84f866d7e432` reached four-node running. Four
UID-1000 QEMU processes used KVM, one management user-NAT NIC, one private
bridge NIC, one root disk and four data disks each. Four taps attached to
`piglet0`; the lease recorded all fixed addresses, dual MACs, VM UUIDs, QMP
paths, invocations, and process identities.

## Network and SSH contract

Public NAT `piglet exec` on each node proved:

- login `vagrant` UID 1000, x86_64, one vCPU, approximately 2 GiB memory;
- exact private address `.10` through `.13` on UP `private0`;
- only `mgmt0` supplied the default route and DNS/Internet;
- peer traffic routed through `private0`;
- `private0` had neither default route nor DNS server;
- outbound HTTPS status 200 and a generation-1 ready marker with the exact
  project/spec identity.

Host ICMP to all four fixed IPs passed with zero loss. Direct host SSH to every
private IP as `vagrant` passed with a separate known-hosts file. NAT product
exec covered all four independent 2222–2225 endpoints. `minio-1` had the
control-only vagrant:vagrant mode-0600 private key and successfully SSHed to
`minio-2`, `minio-3`, and `minio-4` over their private names. The three worker
guests had no control private key.

## Sixteen data-disk contracts

For every persisted data-disk serial, the test resolved
`/dev/disk/by-id/virtio-<serial>`, followed the link to the expected device,
verified an exact 34,359,738,368-byte block size, matched the mounted source,
read the XFS UUID, found the same UUID/mount/filesystem plus `nofail` in fstab,
and wrote a unique persistence canary.

| Node | Disk | Serial | Device | UUID | Mount |
|---|---|---|---|---|---|
| minio-1 | data1 | `enafhhw7xhunluzuwiaa` | `/dev/vdb` | `1a556dfb-589b-43ab-96f0-c45fe8a88893` | `/data1` |
| minio-1 | data2 | `dok5xd63ixfkuktadcqa` | `/dev/vdc` | `7b39aef2-0282-4e58-a6d3-692199b00ba0` | `/data2` |
| minio-1 | data3 | `cgwof56az5libqxcrvwq` | `/dev/vdd` | `b23b6e77-a6e2-417f-a002-b521d49da12a` | `/data3` |
| minio-1 | data4 | `ywsdjgnhxam4a23p4tzq` | `/dev/vde` | `f2ffc42e-39fb-40ce-80d3-07c5c8ebbb5d` | `/data4` |
| minio-2 | data1 | `3uydhx2d3amwzpn35fnq` | `/dev/vdb` | `c086e0b0-a03a-4701-b3ea-c5198527c827` | `/data1` |
| minio-2 | data2 | `giow2eul5phl27binrma` | `/dev/vdc` | `2da5342c-46e3-47af-a317-684c4611742f` | `/data2` |
| minio-2 | data3 | `5iakn5zqmnl2uxtvykca` | `/dev/vdd` | `d170cd08-3157-46f8-aaac-33a560759c31` | `/data3` |
| minio-2 | data4 | `inhgd22lpjynakbxoaja` | `/dev/vde` | `b198d116-4ebf-40f4-b673-1df5991ce33f` | `/data4` |
| minio-3 | data1 | `ssn3obco6zpklex4eakq` | `/dev/vdb` | `3fa7996d-9c7e-49f9-a920-dab17226485c` | `/data1` |
| minio-3 | data2 | `6yc2mpvpvubw47c2ox4a` | `/dev/vdc` | `9853ba50-a70c-48d5-a674-a9d82290e463` | `/data2` |
| minio-3 | data3 | `i4qdwc6g3aiu4nnmooia` | `/dev/vdd` | `99458068-2e7e-4ef0-a7d1-7f41e7bf8b3f` | `/data3` |
| minio-3 | data4 | `vkzrefxwm7pz3wttplca` | `/dev/vde` | `78a5a796-b2dc-497b-9b23-7745b34a4f81` | `/data4` |
| minio-4 | data1 | `ufunrfj5bkxmlayg7yya` | `/dev/vdb` | `a065dff5-6823-4832-bf38-7d2ab6be5cd3` | `/data1` |
| minio-4 | data2 | `w27hyeoqm4okgx3woqxq` | `/dev/vdc` | `bc2cb634-23c0-4c07-bb1d-b055a24654d2` | `/data2` |
| minio-4 | data3 | `jsdcxtravqwkserxtzoa` | `/dev/vdd` | `8978e664-ba8e-4a46-ba55-17c9388ed83e` | `/data3` |
| minio-4 | data4 | `bn6gysh7q5s2ai7lfxya` | `/dev/vde` | `a547972b-124c-4264-832c-9b86bd7f71a1` | `/data4` |

The machine-readable complete table, including each canary SHA-256, is
`artifacts/disk-contract-passed.tsv`.

## Stop/start persistence

Public stop returned four stopped/inactive nodes, removed all four QEMU
processes, taps, project runtime directories, and the active lease. Public
start returned four running nodes with new PIDs while retaining every VM UUID
and invocation hash. Four taps and all four fixed IPs returned.

All sixteen canary SHA-256 values were then recomputed through NAT product exec
and exactly matched their pre-stop values. The authoritative result is
`artifacts/canaries-after-restart-passed.tsv`.

## Final stopped disks and scoped destroy

After the persistence check, a second public stop again left no QEMU, tap,
runtime, or active lease. While stopped, all twenty runtime qcow2 files passed
`qemu-img info --backing-chain` and `qemu-img check -f qcow2`:

- four 64 GiB root overlays, each with explicit qcow2 backing format and the
  same verified mode-0444 base;
- sixteen standalone 32 GiB data disks.

The retained check log contains 20 sections and 20 `No errors were found`
results.

Pre-destroy inventories and hashes were saved. Public
`destroy --force --json` returned four nodes in structured `absent` state and
removed the four node directories, all twenty qcows, seeds, NVRAM, node state,
and node logs. It preserved both project markers, the project lock/events, the
Ed25519 key pair byte-for-byte, and the managed image cache byte-for-byte.
Private destroy intentionally removes `resolved.json`; the first post-destroy
audit incorrectly expected it to remain, and the corrected audit records the
designed absence.

## Public network uninstall and final host state

Public network uninstall dry-run and apply both returned exit 0; apply reported
`restored`. The final host matched the original clean prestate:

- no QEMU, tap, `piglet0`, private route/address, runtime, lease, or partial;
- no Piglet networkd/NM/qemu/tmpfiles config, `/etc/qemu`, `/var/lib/piglet`,
  or `/run/piglet`;
- bridge helper root:root 0755 with no dpkg override;
- four networkd units disabled/inactive;
- NetworkManager enabled/active.

Post-uninstall public network status returned the expected clean-host
capability exit 3. The scoped project marker, mode-0700 key directory, and
mode-0444 digest cache remain inside the retained evidence root. No runtime
disk remains after the requested destroy.

## Audit errors and evidence boundary

Four audit-only mistakes were retained and corrected without changing product
state:

1. nested `cut` quoting failed after the first disk had already passed its
   identity/mount checks and canary creation;
2. a replacement nested `sed` expression had the same quoting issue;
3. the first post-restart TSV loop let nested SSH consume the loop's stdin and
   therefore checked only one canary;
4. the first destroy audit expected private `resolved.json` to be retained.

Fixed-loop runs subsequently passed 16/16 disk contracts and 16/16 restart
canaries; the corrected destroy audit passed. These were orchestration errors,
not VM, disk, network, or lifecycle failures.

The evidence root remains mode 0700 because it contains a generated project
private key. `artifacts/EVIDENCE_SHA256SUMS` covers 144 retained non-qcow input,
log, and evidence files and has SHA-256:

```text
312d29ad9d4228710bdb30db3bd3c88754dfccd1a3ab2751e7ce57711de76013
```

This is one Linux/amd64 u24 MinIO-profile execution. It does not prove the same
profile on Darwin/arm64, another guest family, host reboot persistence, MinIO
application correctness, or GA package installation.

