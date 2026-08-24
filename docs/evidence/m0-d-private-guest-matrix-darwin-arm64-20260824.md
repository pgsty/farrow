# M0-D macOS arm64 private guest matrix — 2026-08-24

Result class: native real E2E. Rocky Linux 9.8 and Debian 13 each passed a
strictly sequential two-node private lifecycle on QEMU 11.1/HVF and the pinned
socket_vmnet host-mode service.

Evidence root:
`/Users/vonng/Library/Caches/piglet/guest-matrix-macos-arm64-20260824-3vQmxk`.

## Rocky Linux 9.8

- Image SHA-256:
  `24692a444f1f0b8bb95375c38c8b43f8099a115347623691be2c330b40c8a1fe`.
- Project: `5fd2b386-8c94-4bdd-996b-34cfa2c02627`.
- Evidence:
  `artifacts-el9/evidence.json`.
- Evidence SHA-256:
  `234fe696da2d6be2749acfee16f277d68d561c6180b55d08512ff13f6dab23aa`.

## Debian 13

- Image SHA-256:
  `2c546c79ec199983a88e384f6e5d013ab7876353943f7aa614403e3028bbea99`.
- Passing project: `30539b18-bfdf-4184-8c06-3833d8680fdf`.
- Evidence:
  `artifacts-d13-retry-after-stale-arp/evidence.json`.
- Evidence SHA-256:
  `9090f3e07b8a193585c474950d3137d8c996e28ff30edbb8b7c84f9a6bd1cc06`.

Both guests passed two distinct MAC-matched NICs, static `.10/.11`, exact
private interface UP/no-default/no-DNS, host→both VMs, VM→VM over private0,
management default route/DNS/HTTP 200, control-only key/lateral SSH, root/data
initialization, daemon restart/reconnect, UID-501 QEMU, QMP shutdown, and no
runtime/QEMU/active-lease residue.

## Retained stale-neighbor failure

The first Debian run booted and completed cloud-init but host→`.10` timed out.
Evidence `artifacts-d13/evidence.json` has SHA-256
`d46760cec473333fe9682f2c6994132ae11d86a16e4d4dd3c0c0221a67162530`.
The host ARP cache still mapped `.10` to the previous Rocky project MAC
`02:83:14:39:85:95`, while Debian meta actually used
`02:3c:0d:81:08:c8`. Removing only the stale `.10` neighbor made the fresh
retry pass immediately.

This exposed a real sequential-project lifecycle bug. The guest private-ready
contract now actively pings the configured host address before publishing the
ready marker. That both verifies guest→host L2 and sends an ARP request with
the new guest IP/MAC, refreshing the host neighbor entry before host-side
reachability is used. Validation requires host/guest addresses in the same
subnet, and unit coverage asserts the refresh occurs before ready.

The final matrix scan found no QEMU/harness process, project runtime directory,
active lease, or `.10/.11` ARP residue. All sensitive image/seed/key artifacts
remain in their mode-0700 evidence roots.
