# Doctor — macOS arm64 — 2026-08-23

Historical snapshot note: this record predates the later root-owned
socket_vmnet installation. Current native host-mode evidence is linked from
[`m0-b-darwin-private-native-20260823.md`](m0-b-darwin-private-native-20260823.md).

Result class: native read-only product CLI integration for M1 host, project,
storage, and private-network diagnostics. Linux/KVM diagnostics are compile-only.

`piglet doctor --json` from the repository (no current project) reported:

- Tier-1 Darwin/arm64, QEMU/qemu-img 11.1.0, HVF, `virt`, host CPU,
  required virtio devices, user netdev, UEFI pair, and OpenSSH;
- resolved default data root and 1128.3 GiB available on its filesystem;
- no current project marker;
- root-owned socket_vmnet not installed, explicitly noting that user-mode quick
  is unaffected;
- 10.10.10.9 follows the default route, so no specific private-subnet conflict
  is currently detected.

From the stopped native product project it additionally reported exact project
UUID/state root/data root, `quick`/`user` resolved identity and spec hash,
`meta` stopped at generation 5, materialized SSH port, no pending journal, and
the same helper/route facts.

Project/data-root discovery does not create directories or lock files. A unit
test compares registry contents before/after. Invalid marker/state/journal,
partial privileged installs, wrong root ownership/modes, a non-listening owned
socket, specific route conflict, and low disk space have distinct error/warning
messages and actionable scoped fixes.

At this snapshot the privileged service was not run because sudo authorization
was absent. That blocker later closed. Current ordinary-user network status
verifies daemon/plist/listening socket and route, while warning that root-only
`network.json` metadata is not directly inspectable. Doctor never suggests
running QEMU as root.
