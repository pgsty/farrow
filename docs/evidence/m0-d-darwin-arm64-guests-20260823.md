# M0-D guest-family smoke — macOS arm64 — 2026-08-23

Result class: native real E2E passed for the user-network guest slice on u24,
el9, and d13. A later u24 private-NIC/control-lateral-SSH run also passed after
the privileged macOS network install. Private el9/d13 remain not run.

## Common host/runtime

- macOS 26.5.2 arm64, QEMU 11.1.0, HVF, `virt`, UEFI
- QEMU UID 501, `dba` login user, per-run Ed25519 key
- 64 GiB root overlay, 64 GiB data disk, read-only CIDATA
- User NAT, DNS/outbound TCP, NTP, QMP stop/start/final stop

## Images and results

| Guest | Source/build | Trust digest | Result | Data filesystem | Evidence |
|---|---|---|---|---|---|
| u24 | Canonical Ubuntu Minimal 24.04 release 20260801 | SHA-256 `3a42e0355636bcc4820af28f5bd2c9591502613ab238ad4fa6d4c3659c03d9cf` | 10/10 lifecycle gate passed | ext4 | `/Users/vonng/Library/Caches/piglet/evidence/quick-10x-20260823-1839` |
| d13 | Debian 13 genericcloud current 20260819-2575 | upstream SHA-512 `3884d98c2f0e1e4134d4d70ff81d82cc953ca516f264057fe4590efed4b215bf28983bd98dbc0ef60dc6284c726b95010c6db3df052122f657ea7e78a2c95363`; local SHA-256 `16bf1413af2eda1c5fe68afd05958157669e0da8742dd9fef780669fe54f83a1` | passed | ext4 | `/Users/vonng/Library/Caches/piglet/evidence/d13-arm64-20260823` |
| el9 | Rocky Linux 9.8 GenericCloud Base 20260525.0 | SHA-256 `24692a444f1f0b8bb95375c38c8b43f8099a115347623691be2c330b40c8a1fe` | passed | XFS | `/Users/vonng/Library/Caches/piglet/evidence/el9-arm64-20260823` |

Every run verified:

- native `aarch64` and SSH login as `dba`;
- generation/spec-hash readiness marker;
- root filesystem grown beyond 60 GiB;
- data disk resolved through `/dev/disk/by-id/virtio-<20-char-serial>`;
- filesystem UUID plus `nofail` in fstab;
- `/data` persistence canary after stop/start;
- DNS, outbound TCP HTTP response, and `NTPSynchronized=yes`;
- QMP identity and clean lifecycle;
- no QEMU/listener/runtime residue.

The filesystem result confirms the guest-family `auto` policy: XFS when Rocky
provides `mkfs.xfs`, ext4 on the Debian/Ubuntu images.

## Private follow-up

u24 native private evidence now verifies two distinct MAC-matched NICs,
`10.10.10.10/.11`, no private default route/DNS, host-to-VM, VM-to-VM,
management internet, and control-only lateral SSH. See
[`m0-b-darwin-private-native-20260823.md`](m0-b-darwin-private-native-20260823.md).

## Not yet satisfied

- private NIC/control behavior for el9 and d13;
- cross-family product private CLI/controller behavior.

Those remaining items must not be inferred from either user-NAT evidence or
the u24 private pass.
