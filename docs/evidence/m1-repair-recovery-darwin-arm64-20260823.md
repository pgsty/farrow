# Repair and crash recovery — macOS arm64 — 2026-08-23

Result class: native real E2E plus repeated integration/unit safety tests for
the M1 single-node repair path. This does not establish Linux/KVM or interrupted
destroy recovery.

## Recovery contract exercised

`piglet repair` is a dry-run unless `--force` is explicit. It now:

- gives matching QMP name/UUID first authority and reconstructs the exact
  PID/executable/start-time/argv identity from the project pidfile;
- converges `starting`, `running`, or `stopping` state to `stopped` only when
  neither QMP nor the recorded process identity is live;
- refuses a live matching QEMU process without usable QMP identity;
- rolls back only a state-less `absent -> preparing|prepared` journal whose
  action names and paths match the exact node allowlist;
- preserves project keys and refuses transitional destroy state, symlinks,
  unexpected file types, unexpected directory entries, unknown journal
  actions, responding orphan QMP sockets, and live orphan PIDs;
- performs the complete ownership/runtime refusal preflight before deleting
  any transaction-created node resource.

## Native live-QMP recovery

Existing isolated product project:

```text
project: /Users/vonng/Library/Caches/piglet/product-e2e/project-njT9Zx
data:    /Users/vonng/Library/Caches/piglet/product-e2e/data-i3Lov9
```

The integration test started the existing u24 arm64 VM under HVF, verified its
QMP and process identity, then atomically fault-injected `phase=starting` with
an empty persisted process identity. This represents a CLI crash after QEMU and
its pidfile are live but before the final state write.

Results:

- dry-run reported one `update-state` action and left the broken state intact;
- forced repair restored `phase=running` and the exact original process tuple;
- the recovered tuple passed a fresh live process-identity comparison;
- normal guest shutdown using the reconstructed identity succeeded;
- final state is `stopped` with a zero process tuple and no runtime directory;
- both root and data overlays passed `qemu-img check`.

Command and result:

```text
PIGLET_REPAIR_E2E_PROJECT=<project> go test ./internal/quick \
  -run '^TestIntegrationRepairReconstructsLiveProcess$' -count=1 -v
--- PASS: TestIntegrationRepairReconstructsLiveProcess (15.78s)
```

## Dead-runtime and orphan rollback probes

A stopped product state was fault-injected to claim `phase=running` with a
nonexistent PID. Dry-run made no change; forced repair converged it to stopped,
cleared process identity, and retained clean root/data disks.

The ownership-bounded orphan tests ran 20 consecutive repetitions. They prove
that dry-run is non-mutating, force removes only resources recorded by a valid
prepare transaction, and preflight preserves every resource when it encounters:

- an unexpected node-directory entry;
- an expected disk path implemented as a symlink to an external canary;
- a live PID in the exact orphan runtime directory.

Project keys and external canaries remained byte-identical in every case.

## Regression gates

After recovery changes, `make check` passed:

```text
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=arm64 go build ./...
```

Remaining recovery work includes interrupted scoped destroy retry, stale temp
and port-reservation coverage as those resources are introduced, Linux/KVM
native execution, and the longer crash/soak matrix.
