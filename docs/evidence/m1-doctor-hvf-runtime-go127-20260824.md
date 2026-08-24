# Current doctor real-HVF/runtime gate — 2026-08-24

Result class: **native Darwin arm64 product PASS** with Go 1.27 source.

`piglet doctor --json` now starts a bounded, paused, diskless QEMU process with
the native machine/CPU and `accel=hvf`, verifies its random UUID/name over QMP,
quits it through QMP, waits for the exact child, and removes its mode-0700
temporary socket directory. Command cancellation owns the foreground child, so
a failed probe cannot leave an unverified daemon.

The current run returned exit 0 and every check was `ok`, including:

```text
qemu 11.1.0 at /opt/homebrew/bin/qemu-system-aarch64
hvf-real-smoke: started virt with hvf and matched QMP name/UUID
qemu-img 11.1.0
arm64 code/vars firmware pair
OpenSSH client
1027.1 GiB data-root filesystem space
marker-bound 10.10.10.0/24 network: protected, host mode, bridge100, ready
```

Postflight found no accelerator-smoke QEMU process or temporary directory. The
JSON SHA-256 was
`597af66672e0699e467ea1e6c1e9574fe9c1f10cfed5148560435d98a15d30e3`.

The follow-up capability-cache implementation keys static help probes by the
resolved binary path, size, nanosecond mtime, parsed version, host OS, and
architecture. A native two-run check first reported `refreshed`, then `hit`;
the five static checks carried explicit cached evidence while the real HVF
smoke still reran. The owner-only 0600 cache SHA-256 was
`2fb49ee84b76f35d4c06cfa120d295eb715c63b72cc6600a20c9abdb5d0dc480`.
First/second JSON hashes were
`b2ce4b209481da01da4fc85095d2ba6dc66783e32b23663ba123b0dc36dd6899`
and
`03c5a308b9dca0725abef6390d90aa84e552b70ff22ce5cfa05c9e96388c1cc0`.

Doctor now inspects every resolved node and the correct Quick/private journal
kind rather than assuming a single `meta` node. This proves accelerator
initialization, not a guest boot; native guest evidence remains separately
classified.
