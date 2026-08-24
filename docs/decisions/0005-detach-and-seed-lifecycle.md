# ADR-0005: QEMU detach and seed lifecycle

- Status: accepted from real M0 evidence
- Date: 2026-08-23

## Decision

Use QEMU's `-daemonize` in the M0/runtime start path. The launching Go command
waits for QEMU's daemonization result, then treats QMP name/UUID as the primary
identity and records pid, executable, process start time, invoking UID, and
expected argv hash. Lifecycle commands never signal a PID without the stronger
identity path.

Keep the NoCloud seed attached read-only across the VM lifecycle. Do not detach
it after readiness in v1 unless later evidence demonstrates a concrete need.

## Evidence

- Ten fresh u24 HVF cycles plus el9/d13 cycles survived CLI process exit,
  QMP powerdown, restart with the same root/data/seed/NVRAM, and final stop.
- Serial logs remained available and no child zombie/runtime residue remained.
- Direct QEMU Unix stream reconnect survived a fake daemon disappearance and
  listener recreation.
- Go-connected `ExtraFiles` FD=3 remained usable after QEMU `-daemonize`, QMP
  identity succeeded, and QMP quit stopped the daemonized process. The same
  path is now composed through the product private controller: a two-node
  create, host/private/internet checks, stop, redial/restart and final stop all
  passed with persisted `socket,id=private,fd=3` invocations; see
  [`m0-b-darwin-fd-product-20260824.md`](../evidence/m0-b-darwin-fd-product-20260824.md).
- The attached read-only CIDATA did not cause cloud-init generation replay;
  the existing generation-matching marker remained valid after restart.

## Consequences

The implementation has one detach model for user and private runtime chains.
Seed contents remain sensitive at rest and retain mode `0600`; debug bundles
must exclude them. Any future detach change requires native guest/reboot/network
regression evidence.
