# Private integrations and stale-ARP recovery — Darwin arm64 — 2026-08-24

This is native product-CLI evidence from the local Darwin/arm64 Tier-1 host. It
extends, but does not replace, the full private lifecycle evidence. The binary
was built from the working tree with `go build -o bin/piglet ./cmd/piglet` and
had SHA-256 `33c1de01361e4a5cafc546fd5dd8661bf3b83a94773b92cd5f4cb757b876093a`.

## Healthy stopped-project repair and audited lifecycle

The retained two-node project `a9fdac53-157e-4487-9912-3c156e3a32f1` reported
both nodes stopped and `piglet repair --dry-run --json` returned
`blocked:false, actions:[]`. A subsequent product `start` used operation
`34d36a40-c268-4b15-b7d4-471acb420366`; `piglet exec meta -- hostname` and
`piglet exec node-1 -- hostname` returned `meta` and `node-1`. Both fixed
addresses answered ICMP. Product `stop` operation
`44bad90c-9ba6-49be-a5ee-7ec24aee29f5` returned both nodes to stopped and
released the private lease.

`piglet logs --source events meta` exposed the project start record and
`piglet logs --source qemu meta` exposed the corresponding per-node structured
record with the actual QEMU invocation. No key or seed content was logged.

## Multi-node SSH config integration

Using an isolated temporary HOME, the explicit command

```text
piglet ssh-config --install --name lab --json
```

created `~/.ssh` as `0700`, and `config` plus `lab_config` as `0600`. The one
owned fragment contained both connection blocks and the required node/address
aliases:

```text
Host lab-meta meta 10.10.10.10
Host lab-node-1 node-1 10.10.10.11
```

The second install returned `changed:false`. Explicit removal returned
`changed:true`, removed the owned fragment and Include, and left an empty
user config. The isolated temporary HOME was then removed exactly; no real
user SSH configuration was modified.

## `/private/etc/hosts` digest-bound round trip

Before the test, `/private/etc/hosts` SHA-256 was
`853f4ba30d94e5c0c36ae8149fa47f88548e7933f7d8116c31bc01448ef97074`.
The dry-run plan named only project `a9fdac53-157e-4487-9912-3c156e3a32f1`
and the following two lines:

```text
10.10.10.10 meta
10.10.10.11 node-1
```

After explicit `hosts install --yes`, the target digest was exactly the
reviewed `5bf9c71f6f856f17ed9bd65a1623fbaa792f9610c08da8304372b5232b2cafb4`.
A repeated install was a non-privileged no-op with `changed:false`. Existing
unowned lines, including the old `pg-meta` and `pg-test` mappings, remained
unchanged. Explicit `hosts uninstall --yes` removed only the matching UUID
block and restored the original byte-for-byte SHA-256
`853f4ba30d94e5c0c36ae8149fa47f88548e7933f7d8116c31bc01448ef97074`.

The privileged helper accepted only `/private/etc/hosts`, rechecked the
reviewed before digest, validated that bytes outside the project marker block
were identical, and atomically published a root-owned result.

## Sequential-project stale ARP regression

After stopping the old project, the host ARP cache still mapped the private
addresses to its old private MACs:

```text
10.10.10.10 -> 02:62:f2:e0:de:51
10.10.10.11 -> 02:73:a4:82:bb:e0
```

Without flushing ARP, a fresh current-code project
`a8466c34-b8dc-4427-8c76-21b1213dc5ab` was created on the same addresses.
The guest readiness finalizer's host-address ping refreshed the host neighbor
entries to the new QEMU private NIC identities:

```text
10.10.10.10 -> 02:1b:8e:64:c0:3f
10.10.10.11 -> 02:ce:21:18:c4:d3
```

Both addresses answered host ICMP, and direct host SSH with the user SSH
configuration disabled (`ssh -F /dev/null`) authenticated with the new
project key and verified the expected hostnames for both nodes. Product
`destroy --force` operation `91899727-9444-442d-9402-c78913c3068e` then
removed both node directories and resolved state and released the lease. It
intentionally retained only the scoped marker, keys, audit log, lock, and empty
nodes directory. The retained audit log SHA-256 is
`4108bc6ed18af091e56d777755170212c07765f147a9623bd12073f1479d5c4d`.

This regression specifically proves sequential private projects can reuse the
fixed IPs without a manual host ARP flush. It is not a substitute for the
remaining `full` profile, crash, reboot, or soak gates.
