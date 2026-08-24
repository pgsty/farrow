# M0-C Linux amd64 private install/E2E/uninstall — 2026-08-24

Result class: native real E2E. A clean Ubuntu 24.04 NetworkManager-only host
completed install → non-root helper attach → two VMs → repeated-plan snapshot
→ uninstall, with its original networkd/helper/config state restored and no
Piglet host-network residue.

## Runner and protected baseline

- Host: `vonng-aimax`, bare-metal Ubuntu 24.04.3 LTS, kernel
  `6.17.0-35-generic`, x86_64, AMD KVM.
- QEMU/qemu-img: 8.2.2; Go: 1.26.7 linux/amd64.
- NetworkManager was enabled/active. The four networkd units were all
  disabled/inactive. `piglet0`, `10.10.10.0/24`, `/etc/qemu`, Piglet config,
  lease/state paths, and dpkg override were absent.
- Helper was package-owned `/usr/lib/qemu/qemu-bridge-helper`, root:root 0755.
- Existing NetworkManager connections, `virbr0`, Docker bridges, failed
  `dnsmasq.service`, and failed `minio.service` were recorded as out of scope.
- The existing `/data/pgsty/piglet` is a 57 GiB dirty Pigsty tree and was never
  written. All work used `/data/piglet-v1-e2e-20260823-2330`.

Repository identity was unborn `main`; no commit is claimed.

## Ordered install transaction

The unprivileged native stager captured exact systemd unit states, helper
metadata/package ownership/override, bridge.conf existence, NetworkManager
state, and target absence. It produced a rootfs staging tree plus a strict
manifest. Initial plan evidence:

- `/data/piglet-v1-e2e-20260823-2330/artifacts/linux-net-stage/install-plan.json`
- SHA-256:
  `52f888a6dba0ca512a014a4928094a2f1ad64dbca38643534dab6c039e4b64a9`.

The installed allowlist was:

| Target | Owner/mode | SHA-256 |
|---|---|---|
| `/etc/NetworkManager/conf.d/90-piglet-unmanaged.conf` | root:root 0644 | `e0281a5755245d35c185270985fb57c736ea85d24cfa2c49651e1ffdb1182b0e` |
| `/etc/qemu/bridge.conf` | root:root 0644 | `efd54eea70ce658af6073bd36d6604e1db21e42292e37c6abea0ff58debf51ad` |
| `/etc/systemd/network/80-piglet0.netdev` | root:root 0644 | `12266d3b284bf9983782c18ad45305c2231658050c722a701f05b108d485cce2` |
| `/etc/systemd/network/80-piglet0.network` | root:root 0644 | `4aacffc72815ae42d16114821b04abe9e1829714dfea99cf12968a8ce07f96ec` |
| `/etc/tmpfiles.d/piglet.conf` | root:root 0644 | `6f57659097f8e539105e5f11a20fff03a06275860d35471d85bc7a85e11d0b06` |
| `/run/piglet/private-lease.lock` | root:root 0666 below root-owned 1777 | empty SHA-256 `e3b0c442…b855` |
| `/var/lib/piglet/network.json` | root:root 0600 below root-owned 0700 | `4ddeb7445e4a168e65d86aa6c13fabd54bc39bd6e1f11693f8c986a0a7158032` |

The order was: persist prestate → install the exact files → reload the NM
unmanaged rule before bridge creation → start networkd without enabling it →
verify only `piglet0` became managed and owned `10.10.10.1/24` → apply
`dpkg-statoverride root:kvm 4750` → create runtime boundary → run non-root VM
attach. Networkd was enabled only after native attach/two-VM success.

## Retained fail-closed result

The first Linux private run (`2026-08-23T15:53:30Z` to `15:58:32Z`, project
`284c0032-e345-4d7a-9229-927f453c3f7f`) proved UID-1000 QEMU/helper attach and
`tap0` membership, then correctly refused readiness. A disk-script refactor
called `mount /data` before publishing its fstab entry; cloud-final failed and
no ready marker appeared.

- Evidence:
  `/data/piglet-v1-e2e-20260823-2330/artifacts/private-linux-m0/evidence.json`
- SHA-256:
  `5f418152d5ccb098ebea82329f722eaa2c3545b54a21d0664fbe587faed581f4`.

The harness timed out, QMP-verified shutdown ran in its defer, and checks found
no QEMU, tap member, QMP/pid, or runtime-directory residue. The fix publishes
the exact UUID fstab entry before mounting and has a regression-order test.

## Passing two-VM run

The fixed binary SHA-256 was
`0fab0b34ef373999e0eecb24e22e7a6ba7c78d0a6f1b8cffc295e67a4af77e39`.
The native command was:

```text
bin/piglet-private-m0 \
  --image <mode-0444 u24 amd64 digest cache path> \
  --sha256 0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe \
  --work-dir /data/piglet-v1-e2e-20260823-2330/artifacts/private-linux-m0-fixed \
  --ready-timeout 300s \
  --linux-bridge-helper /usr/lib/qemu/qemu-bridge-helper
```

The run lasted from `2026-08-23T15:59:04Z` to `15:59:37Z` and passed:

- two KVM VMs at `10.10.10.10` and `.11`, QEMU UID 1000;
- helper-created taps on `piglet0` while running;
- host-to-both-VM and VM-to-VM private route/ping;
- both VMs used management NAT for default route, DNS, and HTTP 200 internet;
- exact private interface/address UP, no private default route, no DNS;
- control-only key and `meta` → `node-1` lateral SSH;
- QMP shutdown and no QEMU/tap/QMP/pid/runtime residue.

Evidence:
`/data/piglet-v1-e2e-20260823-2330/artifacts/private-linux-m0-fixed/evidence.json`,
SHA-256
`f34deacb718b6fcbe4be03778ae29162d0ed5ca5a65014647520cfd252d944bc`.

## Repeat and uninstall restoration

After enabling networkd, a root-owned copy of the stager read the root-only
manifest. Its repeated plan preserved the original disabled/inactive snapshot
and produced a byte-identical `network.json`; it omitted helper override,
networkd start, and networkd enable. Repeat plan SHA-256:
`d1cbf496b50bff37d3d036342f428ce46528652ea1e76bd586e907b48593e3ff`.

Before uninstall, exact hashes, helper override, empty lease, absence of QEMU,
and absence of bridge members were reverified. Uninstall then:

1. deleted `piglet0` while NM still considered it unmanaged;
2. unlinked only the exact owned networkd/NM/tmpfiles/bridge files;
3. removed the exact dpkg override and restored helper root:root 0755;
4. restored all four networkd units to disabled/inactive;
5. unlinked the empty owned lease lock and root-only state last;
6. used only `rmdir` for the newly created, proven-empty Piglet directories;
7. removed the exact root-owned stager copy and its proven-empty `/opt/piglet`
   directories.

Postconditions passed: no `piglet0`, `10.10.10.0/24` route, Piglet config,
override, lease/state/lock, `/etc/qemu`, `/var/lib/piglet`, `/run/piglet`, or
`/opt/piglet` remained. NetworkManager stayed active with the same device/
connection list and routes. The pre-existing dnsmasq/MinIO failed states were
unchanged. All `/data/piglet-v1-e2e-*` artifacts were retained.

## Remaining boundary

The M0-C clean-host spike is passed. Product `piglet network install/uninstall`
does not yet expose this ordered executor; ordinary-user status can verify the
bridge/helper/route but warns that mode-0700 state needs privileged metadata
verification. Product private controller/lease, reboot persistence, 30-cycle
soak, and public CLI E2E remain in progress.
