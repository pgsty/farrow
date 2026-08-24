# Persistent disk and project-key retirement — Darwin arm64 — 2026-08-24

Result class: **native public product lifecycle E2E** for the new persistent
disk store, explicit deletion flag, compatible reattachment/recreate, and
`project purge-keys` boundary. User-mode NAT was used; no global network or
privilege was involved.

## Identity

- Window: `2026-08-24T07:06:37Z`–`07:07:46Z`.
- Retained mode-0700 root:
  `/Users/vonng/Library/Caches/piglet/persistent-quick-darwin-go127-20260824-01`.
- Piglet SHA-256:
  `465cadd5b7615bb3aeeb88a655d7ce2effd958d2d007d8dbabc37d49408738de`.
- u24 SHA-256:
  `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476`.
- Strict persistent user-mode config SHA-256:
  `668fef1408d7c3a081c5693cb0ac55f63535889d2c84c2e36b159d7293f744bc`.
- Project: `f52e5af9-06dd-41d8-b785-6cd57caa0893`.
- Evidence checksum-list SHA-256:
  `04712bd62cda3602b46d10c26b58ab6164d06d5ba1688513e61d5c0f9ba87587`.

## Preserve and reattach

The config declared one 64 GiB `/data` disk with `persistent: true`. After the
guest wrote a unique canary, ordinary `destroy --force` returned the node
absent and moved the disk from the node directory to:

```text
persistent-disks/meta/data/disk.qcow2
persistent-disks/meta/data/ownership.json
```

The mode-0600 strict marker bound project/node/disk, canonical path, UID,
deterministic serial, exact 64 GiB size, and `/data` mount. The retained qcow2
passed `qemu-img check`; the node directory was absent.

Public `up -f` reused that exact retained path and preserved the canary.
`recreate --force` on the running node then performed its real
stop→preserving-destroy→up path, reused the same record again, and preserved the
same canary a second time.

## Explicit deletion and key purge

`destroy --force --delete-persistent --json` supplied the separate destructive
confirmation, destroyed the node, then deleted exactly one validated retained
disk/store. Output stated:

```text
destroyed node artifacts; image cache, project marker, keys, and persistent
data disks preserved; explicitly deleted 1 persistent data disk(s)
```

No persistent store or node directory remained. `project purge-keys --json`
then returned a three-path dry plan without mutation. Only
`project purge-keys --yes --json` removed the exact marker-owned
`id_ed25519`, `id_ed25519.pub`, and `known_hosts`, with every action marked
applied. Both project markers and the immutable image cache remained; no
project QEMU process remained.

The implementation's unit/race coverage additionally rejects incompatible
size/mount/serial, unexpected entries, path escape, symlink/hardlink targets,
deletion before destroy, private deletion with an active lease, and key purge
while node or persistent artifacts remain.

This run covers Quick/user-mode persistent storage and the shared key-purge
boundary. Private persistent behavior is covered by the same strict store and
unit/race integration, but has not yet had a separate native private-VM disk
run.
