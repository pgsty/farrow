# `--no-wait` and verified signal fallback — Darwin arm64 — 2026-08-24

Result class: **native scoped fault-injection E2E**. A current Quick VM was
started with `--no-wait`, its own QMP socket was deliberately unlinked after
the product had persisted complete process identity, and public stop proved the
QMP-unavailable signal fallback before scoped destroy.

- Window: `2026-08-24T07:20:58Z`–`07:21:05Z`.
- Mode-0700 root:
  `/Users/vonng/Library/Caches/piglet/nowait-signal-darwin-go127-20260824-01`.
- Piglet SHA-256:
  `b77de089f161e25917edae4730f814b272fa5454d22297b76beecdd1db4040ca`.
- Project `e7bed17e-a483-4272-9485-62f735276f0c`, PID 36926.
- Evidence checksum-list SHA-256:
  `8d9c981dd2090d40fa8fefd6a6e25d205db4ff8adb3be6e60b05a5791f595986`.

`up --no-wait --json` returned running in one second after QMP name/UUID and
process executable/start-time/argv identity were verified; it did not wait for
SSH or the guest ready marker.

The test accepted only the exact runtime socket derived from marker-owned state:

```text
/tmp/piglet-501-e7bed17e-meta/qmp.sock
```

After unlinking that one socket, `stop --json` could not use QMP. The lifecycle
revalidated every persisted process/invocation field, sent SIGTERM to the
verified QEMU, waited boundedly, observed PID exit, removed runtime artifacts,
and returned the node stopped. No SIGKILL was needed. Public destroy then
returned absent and preserved cache/keys.

Deterministic unit/race tests separately cover QMP powerdown/quit ordering,
incomplete or mismatched identity refusal, SIGTERM timeout followed by
revalidation and SIGKILL, PID reuse rejection, and bounded SIGKILL failure.
The initial fault injection exposed and fixed an old Quick-manager precheck that
had made the new low-level fallback unreachable; the retained run uses the
corrected complete call chain.
