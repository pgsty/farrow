# Darwin composed socket FD fallback — native product controller — 2026-08-24

Result class: **native real E2E** on Darwin/arm64. This closes the composed
Go-dial + `ExtraFiles` descriptor-3 fallback gap; it is distinct from the
earlier direct-QEMU FD probe.

## Implementation under test

The ordinary product private controller now selects:

- QEMU 10.2+ with `stream` support: Unix stream plus `reconnect-ms=1000`;
- QEMU 8.2.1–10.1, or a build without `stream`: Go dials the verified
  root-owned socket_vmnet socket and passes the connected file as child FD 3,
  with QEMU `-netdev socket,id=private,fd=3`.

The test-only `piglet-private-fd-m0` command forces the latter selection while
using the same product project, image, disk, seed, lease, state,
start/stop/QMP/SSH controller code. It does not implement a second runtime.

Relevant exact hashes:

```text
7cd3196c01fa64a004fca1e1d31564fd23de6fffc86a418e0c1a2d9510d73b3e  bin/piglet-private-fd-m0
3284c19a11b71f4294b1ea5aaf84b4f265270b518fe5f07beeba8dbcbf546fce  internal/execx/execx.go
647fc1763103b0e6b967a73b26b58422602ade113828bff43a858c985d8e8ade  internal/vm/lifecycle.go
03dc7e264d468892395e3781f42341b5fb4df81910f42e5550ed220059a0b4e1  internal/private/start.go
```

## Native run

The run used the checked-in two-node local-u24 private configuration, isolated
workdir `/Users/vonng/Library/Caches/piglet/fd-product-darwin-pOBwsm`, and the
existing digest cache. Project `4ebba480-f862-4160-904b-74c03ce98c09` reached
ready on both nodes. Persisted state for both `meta` and `node-1` contains the
exact FD backend and no stream backend:

```text
meta    socket,id=private,fd=3
node-1  socket,id=private,fd=3
```

The harness verified:

- host TCP to `10.10.10.10:22` and `10.10.10.11:22`;
- each guest routed its peer through `private0`;
- each guest reached the internet and returned HTTP 200 through management
  NAT;
- first create/start produced two QMP-verified non-root QEMU processes;
- product stop closed both inherited connections and released the lease;
- product start redialed socket_vmnet, passed fresh FD 3 handles and reached
  both generation-matching ready markers;
- final stop removed QEMU/runtime artifacts and released the lease.

Initial PIDs were 79676/79675 and restart PIDs 80288/80287. The complete JSON
result reported `result: passed` and `lease_absent: true` in 44.7 seconds.
All four stopped root/data qcows passed `qemu-img check`. The 296 MiB project
state/disks/logs are intentionally retained; the workdir contains only its
project marker.

## Boundary

This proves the composed fallback and automatic version/capability selection
logic on the current host by forcing the fallback branch. It does not claim
that current QEMU 11.1 lacks stream support—the normal product path correctly
selects stream/reconnect there. Shared-mode socket_vmnet and host reboot remain
separate checks.
