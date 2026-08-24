# ADR-0006: host-global private lease root and ownership

Status: accepted for implementation; native M2 E2E pending.

## Context

The private network is one host-global resource, while projects may choose
different data roots and may be run by different local UIDs. A lease beneath a
project or `PIGLET_DATA_HOME` would therefore permit conflicting projects.
Conversely, ordinary Piglet must not rewrite root-owned network state or run a
user-writable binary as root.

## Decision

The privileged network installer creates one root-owned mode-1777 runtime
directory:

```text
macOS: /private/var/run/piglet
Linux: /run/piglet
```

The directory contains `private-lease.lock` and `private-lease.json`. The lock
is mode 0666 so all local contenders serialize on the same inode. The lease is
mode 0644 and owned by the active UID. The sticky root permits that owner to
publish same-directory atomic replacements but prevents another UID from
replacing/deleting the lease. Cross-UID stale takeover therefore requires
privileged/operator review; it never silently weakens ownership.

The strict versioned lease records project UUID, owner UID, the installed
global IPv4 `/24` contract, every node IP/MAC/VM UUID, runtime QMP/pidfile,
typed invocation, and captured process identity. Same-project reservation
reentry is idempotent. A different project or changed reservation conflicts.
Updates increment generation and use temp→fsync→rename→parent fsync.

Ordinary release requires every persisted node phase to be stopped and a fresh
QMP/process audit proving all nodes dead. Crash reclaim is separate: a recent
heartbeat has a five-minute grace; after that, matching QMP or process identity
blocks reclaim, unverified live PIDs/inconsistent QMP hard-fail, and only an
all-dead same-UID lease may be atomically replaced.

## Consequences

- Custom project data roots cannot split the global lock domain.
- Host reboot clears the runtime lease along with stopped VMs; the installed
  root-owned network configuration remains persistent.
- A malicious active lease owner can still sabotage its own coordination. v1
  does not claim hostile-local-user isolation, but other UIDs cannot perform a
  normal unprivileged takeover.
- Network uninstall must refuse while the lease file exists or the runtime
  directory contains unexpected entries.
- The installer and Linux tmpfiles/systemd configuration must create and verify
  the exact root/mode before private runtime is enabled.
