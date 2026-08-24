# Private lease foundation — 2026-08-23

Result class: unit, cross-process integration, race/static, and compile-only
evidence for FR-120's host-global lease. This is foundation evidence only;
standalone native M0 two-node runtime later passed on both Tier 1 hosts, but
product controller+real-lease and native M2 E2E are not run.

## Ownership and path contract

ADR-0006 places the lease outside every project/data root under an
installer-created root-owned mode-1777 runtime directory:

```text
macOS: /private/var/run/piglet
Linux: /run/piglet
```

The shared lock is mode 0666 and the atomic mode-0644 lease is owned by the
active UID. The sticky parent permits same-owner temp→fsync→rename while
preventing a different UID from normal unprivileged replacement/deletion.
Cross-UID stale takeover therefore remains a conservative conflict requiring
operator/privileged review.

The strict schema records generation, project UUID, owner UID, one installed
global canonical IPv4 `/24`, host/DHCP boundary, and 1–20 unique nodes with
private address, management/private locally-administered MACs, VM UUID, phase,
runtime QMP/pidfile paths, typed QEMU invocation, and process identity.
Addresses inside the DHCP pool, host/network/broadcast, duplicate names/IPs/
MACs/UUIDs, partial process identity, malformed/overlong runtime paths, unknown
JSON fields, trailing data, symlinks, and owner/mode mismatch are rejected.

## State machine behavior

- first project acquire atomically creates generation 1;
- same-project/same-reservation acquire is idempotent and returns the existing
  runtime state;
- changed reservations or another project return typed lease conflict;
- updates preserve creation identity and increment generation;
- ordinary release requires every persisted node `stopped` plus an all-dead
  runtime audit; repeated absent release is idempotent;
- crash reclaim has a five-minute heartbeat grace, then requires same owner UID
  and all nodes proven dead before atomic replacement;
- another UID, matching live QMP, matching live process, mismatched QMP, or any
  unverified live recorded/pidfile PID blocks reclaim.

The runtime auditor uses matching QMP name+UUID first, then stable executable/
start-time/argv process identity. Reserved nodes with no runtime are considered
dead only after Store-level stale grace permits an audit.

The platform-neutral private intent builder now derives every resolved node's
fixed address, deterministic management/private MACs, control identity, aliases,
short Darwin-safe runtime/QMP/pidfile paths, and reserved lease node. New VM
UUIDs come from an injected cryptographic source; same-project reentry reuses
the UUIDs already persisted in the lease. Exactly one control, unique static
addresses, local-unicast distinct MACs, custom global `/24` boundaries, and
runtime socket length are validated before any host mutation.

Per-node CIDATA rendering consumes that intent. Every node receives identical
node/FQDN host mappings, DHCP/default-route management NIC, static private NIC
without gateway or DNS, deterministic data-disk serials, generation/spec-ready
marker, and the project public key. Only the declared control node receives the
project private key and lateral SSH config. Tests prove a worker seed never
contains the private-key canary. This also exposed and fixed cloud-init host
validation so configured FQDN aliases are accepted without allowing `..`.

The bounded multi-node preparer then turns those intents into journaled offline
artifacts. Nodes prepare independently with default concurrency four; each
exact root overlay, data disk, seed, and NVRAM copy is fsynced/published before
its allowlisted journal action is committed. A typed Darwin stream or Linux
bridge QEMU invocation is recorded only after all artifacts exist. Runtime
directories and QEMU processes are deliberately not created during prepare.
Partial failure preserves successful nodes and the failed node's exact partial
journal for later scoped repair; it does not roll back pre-existing nodes.

Native qemu-img offline integration retained two real prepared Darwin nodes at:

```text
/Users/vonng/Library/Caches/piglet/product-e2e/private-offline-prepare-YaimHp
project: 3294ea7a-c1cd-4f1e-b304-df71cda42ab3
```

Both 8 GiB roots have the verified u24 managed backing, pass `qemu-img check`,
and contain distinct VM UUID/MAC/runtime intent. `meta` additionally has a
backing-free 4 GiB data disk. Both CIDATA images round-trip as `CIDATA`, both
journals are strict/prepared with the same spec hash and stream reconnect
netdev, and no runtime directory or QEMU process was created.

Prepared artifacts can now commit into the existing versioned project/node
state store. Successful nodes become durable `prepared` state even when a peer
failed; the failed peer keeps only its partial journal. Each successful journal
is marked `state_committed` and remains until a mandatory lease verifier
confirms the same prepared VM UUID/spec/invocation. Finalization then removes
only that exact journal and fsyncs the node directory. Commit is idempotent
across a crash between node-state publication and journal acknowledgement.

The start/stop orchestrators now mirror whole-operation phase barriers into the
lease. Start first commits every selected node as `starting`, then performs
bounded parallel QEMU/QMP starts and readiness waits; the final lease generation
records each authoritative `running` or still-`starting` state. A readiness
failure keeps that VM running, and a peer start failure never stops successful
nodes. Stop similarly records `stopping`, performs bounded QMP/process-safe
shutdown and exact runtime cleanup, then mirrors `stopped`/still-`stopping`.
The lease is released only when every leased node is stopped and a required
runtime auditor proves all dead. Unit lifecycles cover full success, start
failure, readiness failure, stop failure, concurrent execution, and release.

The first-create controller composes the complete unprivileged flow: acquire
the reserved lease, bounded parallel prepare, commit every successful node,
mirror prepared runtime intent, lease-verify/finalize journals, and start only
the successfully prepared nodes. Full success and injected peer disk failure
both pass. The latter returns a typed partial-success error naming `node-1`,
keeps `meta` running, leaves the failed node without stable state, and retains
its partial journal/reservation for repair. Private package race tests and
Staticcheck pass across the composed controller.

Explicit failed-prepare rollback is dry-run-first and scoped to only failed
outcomes from that create result. It refuses after stable node-state commit,
when QMP/pid runtime artifacts exist, or when the node directory contains any
unrecorded file/symlink. Tests prove unexpected-entry preflight preserves every
artifact and rolling back `node-1` leaves the successful running `meta` state
untouched.

## Verification

- 24 goroutines contending for different projects: exactly one winner;
- two independent test processes contending on the real filesystem lock:
  exactly one acquire and one exit-6-style conflict, repeated 20 times;
- same-project idempotency and changed-reservation conflict;
- prepared/running/stopped generation updates;
- live-QMP and live-pidfile refusal, stale pidfile/dead observation;
- release dry-run/apply and same-UID reclaim dry-run/apply;
- recent heartbeat and cross-UID reclaim refusal;
- custom `172.30.50.0/24` acceptance and DHCP-pool rejection;
- lease symlink/external canary and non-sticky root refusal;
- deterministic two-node intent and existing-lease VM UUID reuse;
- two-node seed network/hosts/disk contract and control-only private key;
- concurrent journaled prepare, partial-success preservation, and real
  two-node qcow2/CIDATA/NVRAM offline preparation;
- idempotent private project/node state commit and lease-verified journal
  finalization, including partial-peer state preservation;
- bounded start/ready/stop orchestration with whole-lease phase barriers,
  partial-success preservation, and all-dead release;
- composed create controller with typed partial-success semantics and
  successful-node preservation;
- ownership-bounded failed-prepare dry-run/apply rollback that never touches a
  successful peer;
- `go test -race` repeated five times, Staticcheck, and Darwin/Linux arm64/amd64
  test-binary compilation passed.

The macOS installer subsequently created root:wheel mode-1777
`/private/var/run/piglet`; native two-node host-mode runtime passed without
acquiring the product lease. Linux created its equivalent root-owned boundary
during M0-C and removed it during verified uninstall/no-residue restoration.

## Remaining gate

No product lease was created by either standalone native harness. Integration
into private `up`, multi-node phase/heartbeat updates, release after native
stop, active-lease network-uninstall blocking, and the 30-cycle no-leak matrix
remain. The former sudo/Linux-runner blockers are closed.

The current public product boundary remains explicit: private `up` still stops
at its capability adapter rather than invoking the composed controller. A
typed lease conflict is reserved for exit 6 once product runtime integration
is enabled; private failure never falls back to user networking.
