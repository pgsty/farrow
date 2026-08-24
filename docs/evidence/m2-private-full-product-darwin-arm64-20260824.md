# M2 product `full` profile — Darwin arm64 — 2026-08-24

Result class: **native real product E2E** on the Darwin/arm64 Tier-1 host. This
run used the public product CLI and the checked-in `profiles/full.yaml`; it did
not use the two-node M0 fixture.

## Identity and retained evidence

- Work/data/evidence root:
  `/Users/vonng/Library/Caches/piglet/full-product-darwin-QjyhfO`, mode 0700,
  retained size 2.3 GiB.
- Project: `6f9f8b19-7034-447c-8b9c-8588677ca77e`.
- Resolved spec SHA-256:
  `d9dc29ebfea5908d3d368b5b0724035cd098a4eef951072a1ebc5e9eb920fbe2`.
- Profile SHA-256:
  `373ade3b60b88a7742a467d9870d734552abcd0a0ad9087aa0624b6d1efb308d`.
- Final tested Piglet binary SHA-256:
  `dda6e21e2949cfc7e8a757f67ca5a01fbd6ae3c86b1a199304c7a4011e618753`.
- Embedded Ubuntu Minimal 24.04 arm64 base digest:
  `3a42e0355636bcc4820af28f5bd2c9591502613ab238ad4fa6d4c3659c03d9cf`;
  release `20260801`, UEFI, 3,758,096,384-byte virtual size.
- QEMU 11.1 used `virt`, `hvf`, `host`; all four QEMU processes ran as the
  invoking UID 501, never root.

The final project is deliberately stopped with root/data disks, state, keys,
serial/QEMU/events logs and cache retained. There is no active lease, QEMU
process, listener or runtime directory.

## First-run failure retained and fixed

The first exact-profile attempt exposed a real guest-contract bug. Ubuntu
Minimal has no `ping`, so the ARP-refresh addition failed cloud-final with:

```text
/usr/local/libexec/piglet-private-contract: line 27: ping: command not found
```

The partially started state was not hidden. The command was cancelled after
the cause was proven; `repair --dry-run` identified the dead starting node and
lease synchronization, and `repair --force` applied only those actions. A
mixed running/stopped `stop` then exposed and fixed a second recovery gap: the
stop adapter had required every node to be running. The corrected stop accepts
already inactive peers, stops live peers, synchronizes and releases the lease.

The redacted failure bundle is retained as
`artifacts/failed-missing-ping-debug.tar.gz`, SHA-256
`85887a9c7cd6d050935ef78c5879bc3aa98eeabe706137a682e4897c6c772765`.
It contains all four states and available serial/QEMU logs and excludes keys,
seeds and qcow2 disks.

The guest contract now uses ICMP when `ping` exists and otherwise emits a
Bash `/dev/udp/<host>/9` datagram, which triggers the same neighbor refresh
without adding a guest package dependency. Current `render.go` SHA-256 is
`903ebc20b65ed6d7c3483219e3defce6695e44065adaf9eec7890f238590346f`.
A scoped destroy removed the failed nodes, and a fresh current-code create
reused only the marker, keys and validated image cache. All four new seeds
completed successfully.

## Four-node topology and guest contract

The successful create operation `2ff33dae-4e9b-44d2-a6d2-4ff9e2def9bb`
returned:

```text
meta    10.10.10.10  NAT SSH 2222  2 CPU / 4 GiB
node-1  10.10.10.11  NAT SSH 2223  1 CPU / 2 GiB
node-2  10.10.10.12  NAT SSH 2224  1 CPU / 2 GiB
node-3  10.10.10.13  NAT SSH 2225  1 CPU / 2 GiB
```

All four fixed IPs answered host ICMP and direct host SSH with the project key
and user SSH configuration disabled. Product `exec` over each independent NAT
fallback returned the expected hostname, `vagrant` UID, `aarch64`, CPU count
and memory. Every node reported:

- approximately 64 GiB grown root partition;
- exactly 137,438,953,472-byte (128 GiB) `/dev/vdb` mounted at `/data`;
- UUID fstab entry with `defaults,nofail`;
- a generation-1 ready marker with the exact project/spec identity;
- default route only through `mgmt0` and the peer route through `private0`;
- no DNS server on `private0`;
- outbound HTTP 200 through management NAT.

`meta` used the control-only mode-0600 private key to SSH over the fixed private
network to every worker. Workers had no project private key. This proves
private TCP traffic used `private0`, not management NAT.

While the full lease was active, a second workspace attempted
`piglet up -f profiles/meta.yaml` and returned the public lease-conflict exit
code 6 naming the full project. No second VM was created.

## Crash recovery

After product status/QMP identity verification, `node-2` PID 3070 was killed
with SIGKILL to simulate an abrupt host-side QEMU crash. Dry-run repair
reported exactly:

- `update-state-stopped` for `node-2`;
- removal of its stale scoped runtime directory;
- lease synchronization.

Forced repair applied those three bounded actions. Product `start` was enhanced
to accept verified running peers and start only stopped/prepared nodes. It
restarted `node-2` as PID 8844 while the other three PIDs remained unchanged;
all four `/data/piglet-full-canary` files survived and host access to `.12`
returned. The same mixed-state recovery was exercised again after an
interrupted preliminary soak.

## Parallel 30-cycle soak

The preliminary serial adapter took 50 seconds to start four nodes and was not
accepted as the final gate. Product create/start/stop now uses the controller's
bounded four-way concurrency. A new evidence root then completed 30 full
cycles. Each cycle asserted:

- all four nodes stopped/inactive and no project QEMU process remained;
- host-global lease was absent while stopped;
- all four nodes restarted running with positive verified PIDs;
- all four data canaries remained;
- all four fixed IPs answered host ICMP.

Results: 30/30 passed, stop 1–4 seconds, start 12–19 seconds, mean start 13.73
seconds. The table `artifacts/full-soak-30-parallel/cycles.tsv` has SHA-256
`85d34175584643b987f7c72bd63b1a96d20fc9c7a3056d246558b28cf5151b5c`.
The full retained evidence checksum list has SHA-256
`bd2cee44349ce5c09b4fb835a9cbb4d9fcdfc2cc96ed2be7447ca27b1c3b7e1c`.

After a final stop, all eight root/data qcows passed `qemu-img check`; the four
known runtime directories, NAT SSH listeners, project QEMU processes, active
lease, `.partial` and temporary cache files were absent.

## Host integrations

An isolated HOME test installed a single mode-0600 SSH fragment containing all
four `name-prefix`, node-name and fixed-IP aliases. Reinstall was
`changed:false`; explicit removal removed only the owned Include/fragment.

The digest-bound root helper installed the four-line full project block in
`/private/etc/hosts`. `dscacheutil` resolved `meta` and `node-3` to their fixed
addresses. Explicit uninstall restored the exact original hosts-file SHA-256
`853f4ba30d94e5c0c36ae8149fa47f88548e7933f7d8116c31bc01448ef97074`.
The root-owned lock is intentionally retained as disclosed by the plan.

## Evidence boundary

This closes the Darwin/arm64 product `full` and 30-cycle gates. It does not
claim Linux `full`, the remaining 16-entry guest matrix, shared/FD vmnet
fallback, host reboot persistence, Pigsty bootstrap, or GA signing/package
gates; those require separate evidence.
