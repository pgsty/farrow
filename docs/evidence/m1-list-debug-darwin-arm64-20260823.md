# Project list and debug bundle — macOS arm64 — 2026-08-23

Result class: native product CLI integration plus unit/race/static gates for
the M1 read-only project registry, structured operation events, log reader, and
redacted diagnostic bundle.

## Read-only project list

Against the stopped native product project, `piglet list --json` reported:

- the marker-verified data root and project UUID;
- the current project, resolved `quick`/`user` identity, and spec hash;
- node `meta` with persisted `stopped` and independently observed `stopped`;
- image alias and materialized SSH port.

A SHA-256 aggregate over every regular project file was identical before and
after the command:

```text
before 08142261d3871458cce2bc3bb7b4a4a6daf5b700b8e74bf13e4af9a151f8dca6
after  08142261d3871458cce2bc3bb7b4a4a6daf5b700b8e74bf13e4af9a151f8dca6
```

Registry discovery reads only direct UUID roots, requires matching strict
markers, rejects symlink/group-writable roots, and reports malformed entries
without traversing them. Runtime observation gives QMP name/UUID first
authority, then a verified process tuple, and never rewrites state.

## Real debug bundle

Command:

```text
piglet debug bundle --output \
  /Users/vonng/Library/Caches/piglet/product-e2e/debug-bundle-20260823.tar.gz
```

Before creation the CLI listed every planned entry and byte size. It created a
mode-0600, valid gzip/tar archive:

```text
size:   19244 bytes
sha256: 3574bef0d857b5a1991a57543a9edea46a64772277050c4cac9e157f954c56d6
files:  12
```

The allowlist contained the manifest, Piglet/Go/host versions, doctor report,
bounded host interfaces/routes, resolved/project/node state, shell-display-only
QEMU argv, and bounded serial log. Archive inspection confirmed there were no
entries for:

- `seed.iso` or other cloud-init content;
- root/data qcow2 disks;
- project private/public keys or `known_hosts`;
- arbitrary project files or the process environment.

The archive contained no private-key PEM marker. The unit canary placed the
same unique secret into source YAML comments, JSON secret fields, doctor
evidence, host output, serial logs, seed, disks, and a project private key. Ten
repetitions showed that collected text/JSON was redacted and excluded artifacts
were never read into the plan or archive. Archives built twice from one plan
were byte-deterministic, every member was mode 0600, and existing output paths
were never overwritten.

## Structured operation events

Lifecycle and SSH commands now receive a version-4 operation UUID. Event
append uses `O_NOFOLLOW`, a mode-0700 parent check, an exclusive file lock, a
64 KiB line limit, redaction, one JSON write, fsync, and mode 0600. A 32-writer
concurrency test repeated 20 times produced only complete JSON lines; a symlink
target test preserved its external canary.

A real HVF start, successful `exec -- true`, failing `exec -- sh -c 'exit 17'`,
and stop produced eight events across six operation UUIDs. The two exec events
for each command share one UUID, record only argument count and exit status,
and never copy remote command text. The guest failure still returned exactly
17. `piglet logs --source events` returned the same valid JSONL.

A second real bundle included `logs/meta/events.jsonl` and remained mode 0600:

```text
size:   19712 bytes
sha256: 302525c33268383918d839da7c5e20e8acb35bab44dc005ab830b0410e2029e0
files:  13
```

## QEMU lifecycle log

Without changing persisted QEMU argv, the product now writes a separate
mode-0600 JSONL `qemu.log`. A real start/exec/stop produced five valid records:

- shell-display-only typed launch argv;
- matching QMP/process identity and PID;
- generation/spec-hash guest readiness;
- QMP `system_powerdown` request;
- process exit and runtime cleanup.

Records use the same operation UUID as command output/events, are capped at 64
KiB, redacted before a locked append, fsynced, and reject symlink targets. The
VM was stopped and `piglet logs --source qemu` returned the exact five records.
Lifecycle commands accept `--log-level error|warn|info|debug`; filtering applies
only to QEMU diagnostics, while audit events remain unconditional.
Events and QEMU JSONL are capped at 8 MiB. Under the same exclusive append lock,
retention keeps only complete records from the newest 4 MiB before appending;
the stress test verifies bounded size, valid JSON on every line, and the newest
record retained. No auxiliary rotation file broadens destroy/bundle ownership.

A final real mode-0600 bundle included events, QEMU, and serial logs:

```text
size:   21299 bytes
sha256: 052b58edfd236d270237afd6c21f922d698799d58f3746c6bc5d6d5fdb86618f
files:  14
```

## Regression gate

After list and diagnostics changes, `make check` passed unit, race, vet,
Staticcheck, and all four Darwin/Linux arm64/amd64 compile targets.

Remaining FR-140 work is native Linux verification.
