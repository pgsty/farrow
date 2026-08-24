# Darwin vmnet conflict isolation and clean shared-mode pass — 2026-08-24

Result class: **native real E2E pass on an isolated alternate subnet, plus a
confirmed external conflict on the product default subnet**. This evidence
supersedes the causal interpretation of the earlier shared negative run; it
does not erase that historical failed run.

## Host and inputs

- macOS 26.5.2 (25F84), arm64
- QEMU 11.1.0 with HVF
- Go 1.27.0
- socket_vmnet v1.2.2 arm64 archive SHA-256
  `c7bf62308fbcfdc29bdfb8373c9b1951f7ac2396446e4390919796a94972e6dc`
- Ubuntu 24.04 arm64 dated standard image SHA-256
  `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476`
- evidence root:
  `/Users/vonng/Library/Caches/piglet/shared-retest-novbox-20260824.WW9djl`

## Why the old negative result was confounded

The historical bundles did not capture `VBoxManage`, VirtualBox processes,
host-only interfaces, or a clean whole-subnet snapshot. Their host neighbor
table retained `.10-.13` MAC addresses from earlier stopped Piglet projects,
not the MAC addresses of the shared VMs under test. The earlier statement that
`.11` ruled out an external `/24` conflict was therefore unsupported.

The new preflight found no registered/running VirtualBox VM, host-only
interface, or NAT network; the idle VirtualBox GUI and `VBoxSVC` were stopped.
It also found OrbStack 2.0.5 running eleven machines and two Apple
`vmenet`/bridge networks. OrbStack was not stopped because doing so would halt
all eleven user machines; the exact running set was recorded.

A later root read-only audit identified the macOS owner boundary more
precisely:

- `/usr/libexec/InternetSharing` was running as launchd service
  `system/com.apple.NetworkSharing` with active count 2;
- `/Library/Preferences/SystemConfiguration/com.apple.vmnet.plist` recorded
  `Host_Net_Address = 10.10.10.1` and mask `255.255.255.0`;
- the original Piglet UUID and the temporary recovery UUID both still had
  `MAC_USED = true`, while the clean alternate-subnet UUIDs became false after
  their foreground daemons stopped.

This proves a live/stale Apple vmnet sharing allocation on the default subnet.
It does not prove whether the original creator was VirtualBox, a prior Piglet
attempt, or another vmnet consumer. OrbStack's visible active ranges were
`192.168.139.0/23` and `192.168.215.0/24`, so its mere presence is not enough
to assign blame.

With the installed Piglet daemon requesting `10.10.10.0/24`, the daemon failed
to create a host interface. A host-mode attempt logged:

```text
vmnet_start_interface: [1009] (unknown status)
```

The local macOS SDK defines value 1009 as
`VMNET_SHARING_SERVICE_BUSY`: a conflicting sharing service prevents the
interface from starting. A listening Unix socket alone had therefore produced
a false-positive installer/status result. No `10.10.10.1`, `/24` route, or
current guest neighbor entry existed.

## Controlled subnet discrimination

The persistent Piglet launchd service was temporarily booted out without
deleting its files. Two foreground probes used the same pinned executable,
root boundary, mask, DHCP `.8` boundary, and isolated sockets/UUIDs:

1. host mode on `172.31.250.0/24` immediately created `bridge100`, host
   `172.31.250.1`, and the exact `/24` route;
2. shared mode on `172.31.251.0/24` immediately created `bridge100`, host
   `172.31.251.1`, and the exact `/24` route.

This A/B result proves that the observed default-subnet failure is a sharing
service/subnet conflict, not a categorical inability of either host or shared
mode.

## Guest RA correction

The first alternate shared guest revealed a second independent defect:
vmnet shared mode advertised IPv6 DNS on `private0`, while the rendered network
configuration disabled DHCP but did not disable IPv6 RA. The private contract
correctly rejected the injected link-local DNS. Piglet now renders:

```yaml
dhcp4: false
dhcp6: false
accept-ra: false
link-local: []
```

This keeps the private NIC static, without a default route or DNS.

## Clean shared two-node result

The corrected run used shared mode on `172.31.251.0/24`, host `.1`, DHCP end
`.8`, and static guests `.10/.11`. Both QEMU processes ran as UID 501. The run
passed:

- host SSH to `.10` and `.11`;
- exact private addresses and `/24` routes;
- no private default route or DNS;
- `.10` to `.11` route, ICMP, and control lateral SSH;
- management user-NAT HTTP egress from both guests;
- QMP shutdown and zero runtime residue.

The retained evidence JSON is:

```text
/Users/vonng/Library/Caches/piglet/shared-retest-novbox-20260824.WW9djl/shared-alt-subnet-ra/evidence.json
SHA-256 df9c174c7f8bf564b0493244fd5815c5f68b8389a4d50ecfdc9eaffadf960847
```

All four stopped qcow2 overlays passed `qemu-img check`. A deliberately
interrupted earlier diagnostic VM was identity-checked through QMP name/UUID,
then stopped with `system_powerdown`; its empty runtime directory was removed.

## Product consequence and restoration

The categorical public rejection of shared mode was removed. Host remains the
preferred mode, but shared is a valid backend when the selected global subnet
is actually available and the guest RA contract is enforced. Installer and
doctor now require the host address in addition to a listening socket and map
error 1009 to a sharing-service conflict.

Final local Go 1.27 binary SHA-256 values after the correction:

```text
piglet             3ca0e3956a6a2fcf5fb5e630ef12f6694a83913316c5fb59bb1853c73abe47d0
piglet-hosts-helper 4d218aef0f83baf20a9bd78d8e2bc76b1bae084cb0371ab22be6d7f6be6f8942
piglet-private-m0  b87176bd0d6d23f5b0b4542f8a652583470b325e165ce7edb76abd11583ba99c
```

The original persistent host-mode plan was restored byte-for-byte with UUID
`89e9f9e5-60cb-48a0-a739-b7fa6e49cde6`; the socket_vmnet binaries and
`piglet-hosts-helper` retained their original hashes, and no test QEMU, runtime,
lease, or temporary root backup remained. At this checkpoint the restored
default subnet was still externally conflicted, so doctor correctly reported
an error.

The follow-up stopped OrbStack vmgr and Piglet vmnet, allowed
`com.apple.NetworkSharing` to reach active count 0, then re-bootstraped the
original plan. Default `10.10.10.0/24` and its two-node data plane passed again.
The coordinated custom-subnet implementation and evidence are recorded in
[`m2-network-preflight-custom-subnet-20260824.md`](m2-network-preflight-custom-subnet-20260824.md).
