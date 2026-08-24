# OpenSSH space-path host-key isolation regression — 2026-08-24

Result class: **native Darwin arm64 Quick product PASS** against the default
macOS data root containing a space.

## Defect and fix

OpenSSH parses `UserKnownHostsFile` as an internal whitespace-separated path
list even when the outer process argv already contains one `-o` argument. The
old value
`UserKnownHostsFile=/Users/vonng/Library/Application Support/...` was therefore
split, leaving each project `known_hosts` empty and creating the stray file
`~/Library/Application`.

All product/M0 argv and generated-config paths now use one shared OpenSSH
configuration-value quote function. It rejects CR/LF/NUL and emits escaped
double-quoted values. `IdentityFile` and `UserKnownHostsFile` in generated
Quick/private fragments are both quoted. A non-connecting `ssh -G` regression
test proves that a path containing `Application Support` is parsed as one exact
`userknownhostsfile` value.

## Native reproduction

No `PIGLET_DATA_HOME` override was set. Two new workspaces ran sequentially
against:

```text
/Users/vonng/Library/Application Support/piglet
```

Project A (`ad309192-db1d-4171-b3a3-b8b1ec27e356`) created and became ready
on SSH port 2222 with all four default forwards. Its exact project
`keys/known_hosts` was owner-only mode 0600, 98 bytes, and
`ssh-keygen -F '[127.0.0.1]:2222'` found its ED25519 key. `piglet exec -- id`
returned `uid=88(dba) gid=88(admin)`. Stop, destroy, and explicit key purge all
returned exit 0.

Project B (`ab713912-ba32-4eab-b452-eae471be98c7`) then reused the same port
2222 and default forwards. It reached readiness without a host-key mismatch;
its separate 0600 `known_hosts` was also 98 bytes and contained its own ED25519
entry. `exec`, stop, start/readiness, second stop, destroy, and key purge all
returned exit 0. The file remained 98 bytes after restart.

A two-node private project (`0cd8cd01-4a8d-4bb5-82cd-875d4dbc0aa7`) then used
the same default data root. Its first parallel start exposed a separate new
runtime-parent race: concurrent creation treated the second normal `EEXIST` as
failure, leaving node-1 `starting` with no PID. The runtime helper was made
concurrently idempotent and gained a 16-worker race test. On the same preserved
project, scoped `repair --force node-1` proved the process dead, synchronized
only that lease node, and `start node-1` then passed.

The private project `known_hosts` was mode 0600 and 196 bytes, with distinct
ED25519 entries for `[127.0.0.1]:2222` and `:2223`. `exec -- id` returned
UID/GID 88 on both nodes. Generated `ssh-config` quoted both `IdentityFile` and
`UserKnownHostsFile` under the `Application Support` path for both nodes.
Full stop released the lease; destroy and key purge passed. The recovery is
part of the regression evidence rather than being hidden as a clean first try.

Before, between, and after both projects,
`/Users/vonng/Library/Application` was absent. Final postflight found no matching
QEMU process, no runtime directory, and no listener on 2222, 15432, 13000,
18080, or 18443. The shared immutable U24 cache was retained; both test project
keys were purged and node disks/artifacts destroyed.

Guarded evidence is retained mode 0700 at:

```text
/Users/vonng/Library/Caches/piglet/f1-space-regression-20260824.2y0boZ
```

`EVIDENCE_SHA256SUMS` SHA-256:
`5495ebc686079d1b4316a3ec3de43ad13354ac3186b95e5a9d88a7f43c1d1031`.
