# ADR-0003: QEMU 11 netdev capability probe

- Status: accepted from real M0 evidence
- Date: 2026-08-23

## Fact

Homebrew QEMU 11.1.0 arm64 exits from a standalone capability command:

```text
qemu-system-aarch64 -netdev help
qemu-system-aarch64: No machine specified, and there is no default
```

The same binary succeeds and lists `user`, `stream`, `socket`, `bridge`, and
the built-in vmnet backends when invoked as:

```text
qemu-system-aarch64 -machine none -netdev help
```

`-machine none -accel hvf -S -display none -nodefaults -qmp stdio` also
completed a real QMP greeting/capabilities/query-status/quit exchange, with
status `prelaunch`. This is a QMP/accelerator initialization probe, not a guest
HVF boot and not an E2E pass.

## Decision

The netdev capability probe includes `-machine none`. Other help probes retain
their simplest working argv and remain independently tested. Product behavior,
support floors, and the requirement for a real u24 HVF lifecycle are unchanged.

