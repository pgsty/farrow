# Product private lifecycle — macOS arm64 — 2026-08-24

Result class: native real product E2E. This run used public `piglet` commands,
the composed private manager/controller, persisted project/node state, and the
real host-global lease. It is stronger than the standalone M0 harness, but it
does not complete all M2 commands/recovery/soak gates.

## Execution identity

- Repository: unborn `main`; no commit is claimed.
- Product binary SHA-256:
  `5ed30094275f61edef7d213a0ce6ee942316ed223703e8963e4138eaa9971679`.
- Host: macOS 26.5.2 arm64, QEMU 11.1.0/HVF, pinned root-owned
  socket_vmnet v1.2.2 host mode.
- Workspace:
  `/Users/vonng/Library/Caches/piglet/product-e2e/private-product-local-work-JyTY8D`.
- Data root:
  `/Users/vonng/Library/Caches/piglet/product-e2e/private-product-local-data-LqUe7L`.
- Project: `a9fdac53-157e-4487-9912-3c156e3a32f1`.
- Resolved spec hash:
  `c52e8abaef9a7b3ccffcc572275b89daa43a56267bf90f0595fe62ac1ff2591b`.

The local u24 arm64 qcow2 was explicitly imported with the new product path:

```text
piglet image import \
  --sha256 aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476 \
  --name local-u24 --boot uefi --source-user ubuntu \
  /Users/vonng/pgsty/cache/image/u24/noble-server-cloudimg-arm64.img
```

The alias registry is strict mode-0600 JSON; it points only to the mode-0444
digest cache and does not use the original local path as a backing file.
Registry SHA-256:
`12a1aea431e60f7c9486181405ab597a8de90ff5fe184a03bf53cf62ed4edf84`.

## Public command sequence

With `PIGLET_DATA_HOME` set to the isolated data root:

```text
piglet plan -f tests/e2e/private-two-local.yaml --json
piglet up -f tests/e2e/private-two-local.yaml --json
piglet status --json
piglet up -f tests/e2e/private-two-local.yaml --json
piglet stop --json
piglet status --json
piglet start --json
piglet stop --json
piglet start --json
piglet stop --json
piglet status --json
```

The plan returned non-destructive `create`. Fresh `up` created two real VMs:

| Node | Private IP | SSH | VM UUID |
|---|---|---|---|
| meta | 10.10.10.10 | 127.0.0.1:2222 | `2f7d6a62-4b59-4a75-a883-f70c05396f03` |
| node-1 | 10.10.10.11 | 127.0.0.1:2223 | `2df1998b-d2dd-4920-ad50-14f741cbe2fb` |

The second `up` returned `already running` with the same PIDs and identities.
Product status validated QMP name/UUID plus executable/start-time/argv hash;
it did not trust the recorded PID alone.

## Runtime and guest assertions

The E2E directly verified:

- QEMU ran as UID 501 with management user NAT plus private stream NIC;
- real lease phases reached running for both nodes and the second project path
  could not bypass the one-active-project boundary;
- host→both private IPs and VM→VM over `private0`;
- both management NICs retained default route, DNS, and HTTP 200 internet;
- exact private IP/interface UP/no-default/no-DNS contract on both nodes;
- control lateral SSH to `node-1`;
- control `.ssh` directory/key/config were dba:dba 0700/0600/0600;
- root staging key/config were absent after finalization;
- worker had no project private key;
- launchd daemon restart restored direct private SSH and contract checks;
- two public stop→start cycles reused project/node/VM identity;
- `/data/piglet-product-canary` survived a full public stop→start cycle;
- every stop used QMP/process identity, removed runtime directories, preserved
  qcow2/key state, and released `private-lease.json`;
- final state is stopped for both nodes, no QEMU process and no active lease.

Final persisted hashes:

- resolved state:
  `cb41e033342dc0af51bb21f768434efc664aaac1369ee5588ba8c1af667cd950`;
- meta state:
  `970eba122b641f9d7ebe59f09bbb441fdd3a83fa1833be2b17bbf3a0d1646194`;
- node-1 state:
  `5d8a57f6c7d591a05afcc8b05e8095f26ae7971aa25fd6238c3374ad6ba000d1`.

This project's disks, seed media, and keys are intentionally retained as
sensitive stopped evidence. No destroy or purge command was run against this
evidence project.

## Later command-surface follow-up

The same stopped project later verified public per-node `exec` on `meta` and
`node-1`, including remote exit-code 42 passthrough, generated two-host SSH
config, and per-node serial log selection. Unimplemented private repair/debug/
list/installable-SSH-config/key-purge paths explicitly returned capability
without calling quick/meta code.

A separate project under the same data root exercised scoped destructive
semantics:

- stopped `recreate --force` preflighted the complete two-node allowlist,
  removed only non-persistent node qcow2/seed/NVRAM/log/state, retained the
  project key and digest cache, and created new VM UUIDs/disks;
- the old `/data` canary was absent after recreate while the project key digest
  was unchanged;
- final `destroy --force` returned both nodes absent, removed node directories
  and resolved state, released the lease, and preserved marker/key/cache;
- the original evidence project and image cache were not touched.

## Additional failure containment

An earlier parallel attempt used the remote embedded image URL without a
proxy. It was interrupted while still in the pre-project download phase so it
could not later start a conflicting lab. The exact owned `.partial` was
verified inside that new cache and unlinked; it had created no workspace
marker, VM, or lease. No pre-existing cache entry was touched.

## Remaining product gates

This closes fresh private `up`, idempotent up, status, stop, start, daemon
reconnect, data persistence, and real lease lifecycle on macOS. Still pending:
private SSH/exec/logs/debug/repair/destroy/recreate semantics, typed drift,
same-command Linux public E2E, public network install/uninstall executor,
partial-start recovery matrix, host reboot, shared/FD fallback composition,
and 30-cycle soak.
