# Phase 2 design

Phase 1 built a VM runtime that is correct and hard to misuse. Phase 2 is about
making it fast to live in, without giving up what makes it trustworthy.

This document is grounded in a rehearsal run on 2026-08-25 against the current
tree, on both Tier‑1 hosts: macOS 26.5 arm64/HVF and Ubuntu 24.04 amd64/KVM.
Every claim below about present behaviour was observed, not assumed.

## Where Farrow stands

One binary drives QEMU directly. Quick mode needs no privilege. Private mode
gives a fixed-IP multi-node lab behind one explicitly installed host network.
State is versioned JSON, user input is strict YAML, and every destructive path
is ownership- and path-bounded.

What the rehearsal confirmed still works exactly as specified:

| Rehearsal | Result |
|---|---|
| Two-node private lab, `init dual` → `up` | both nodes running, `.10` and `.11` |
| Host → VM | ping both nodes |
| VM → VM over `private0` | ping, 0% loss |
| VM → internet over the management NIC | HTTP 301 through NAT |
| Private NIC contract | `10.10.10.10/24` on `private0`, exactly one default route, and it is not on `private0` |
| Control-only lateral key | `meta-1` → `meta-2` SSH succeeds; `meta-2` → `meta-1` fails host key verification |
| Second lab while one is running | exit 6, naming the holding project and UID |
| Private drift (`cpus` 1→2) | `plan` reports `action: recreate, destructive: true`; `up -f` exits 4 with the exact remedy |
| Partial destroy | refused, exit 2 |
| Stranded `stopping` phase | `repair --dry-run` named the file and its evidence, `--force` fixed it, `start` then worked |
| Teardown | artifacts removed, lease released, cache and keys preserved |

That is a solid foundation. The friction is elsewhere.

## What the rehearsal found

Ranked by how often a Pigsty developer would hit it.

| # | Friction | Observed |
|---|---|---|
| 1 | No host↔guest file sharing | editing Pigsty source on the host means `scp` or a guest-side clone on every iteration |
| 2 | No snapshot or rollback | nothing between "working lab" and "rebuild from scratch" |
| 3 | Cannot reset one node | `destroy` refuses node selectors: *"private destroy currently requires selecting the complete project"* |
| 4 | One lab per host | exit 6 blocks running two Pigsty versions side by side |
| 5 | No resource admission control | nothing checks host memory; `simu` declares 94 GiB and `deci` 60 GiB across their nodes |
| 6 | Projects accumulate forever | nine destroyed projects still listed on the rehearsal host; `project` offers only `purge-keys` and `upgrade-state` |
| 7 | Interrupted stop needs manual repair | the error names the problem but not the command that fixes it |
| 8 | Linux private mode blocked on wireless hosts | stock `80-wifi-adhoc.network` trips the networkd activation proof, exit 7 |
| 9 | No suspend or resume | `stop`/`start` is a full guest reboot |
| 10 | Images pinned to dated upstream URLs | they resolve today; upstream rotation is a scheduled outage nobody has scheduled |

## Invariants Phase 2 must not break

Every proposal below was checked against these. A feature that cannot fit
inside them does not ship.

1. QEMU runs as the invoking user. Quick mode needs no privilege at all.
2. Native architecture, native accelerator. No silent TCG fallback.
3. No arbitrary QEMU argument passthrough, and no global destructive command.
4. Destroy, prune, repair and uninstall stay ownership- and path-bounded.
   Ambiguity preserves data and reports.
5. Strict parsing. Unknown fields are errors.
6. No new runtime dependency without a documented purpose, a pinned version,
   a license review, and a standard-library alternative assessment.
7. A privileged component never executes a user-writable binary or takes a
   shell string.

## Stage 0 — close 1.0 first

Phase 2 code ships after 1.0, not instead of it. The four open gates are
ownership decisions, not engineering:

- self-hosted image storage, domain and bandwidth;
- active and standby custody for the image manifest signing keys;
- a production release identity and a two-person publication review;
- a durable macOS arm64 runner.

The tag workflow, package verifiers and reproducible builds are already in
place and exercised. Nothing in Stage 1 depends on Stage 0, but shipping new
features on top of an unreleasable base compounds the problem.

## Stage 1 — the inner loop

The highest value per unit of risk. All four items are additive and break no
invariant.

Before Stage 1, Farrow now has one intentionally smaller piece of familiar
Vagrant ergonomics: explicit `farrow provision --script ...`. It is a bounded
Bash-over-SSH operation for already-running nodes, not the general provisioner
that v1 explicitly excluded: no Vagrantfile parsing, provider plugins,
interpreter selection, automatic `up` hook, host shell, or hidden once-state.
That closes the immediate Pigsty bootstrap gap without coupling provisioning
state to VM state. The next highest-value Vagrant-like capability remains the
9p host↔guest share below.

### 1.1 Host↔guest source sharing

**Problem.** The core Pigsty loop is *edit on the host, deploy in the lab*.
Today that means copying the tree in on every iteration.

**Design.** Attach an optional 9p share per node. It is read-only unless the
configuration explicitly requests read-write access:

```yaml
nodes:
  - name: meta
    shares:
      - host: /Users/me/pgsty/pigsty     # absolute, canonical, must exist
        guest: /src                      # canonical absolute, non-reserved
        readonly: false
```

Device evidence from the rehearsal — this decided the mechanism:

| Device | macOS QEMU 11.1.0 | Linux QEMU 8.2.2 |
|---|---|---|
| `virtio-9p-pci` | available | available |
| `vhost-user-fs-pci` (virtiofs) | **absent** | available, but needs a `virtiofsd` daemon that Ubuntu does not install |
| `virtio-vsock-pci` | absent | absent |

9p is the only mechanism available on both Tier‑1 hosts with no new host
dependency, so 9p is the contract. virtiofs may later be an opt-in Linux
acceleration, never the baseline. vsock is unavailable on both, which rules out
a vsock-based guest agent for the whole phase — guest communication stays SSH,
serial and the ready marker.

**Boundaries.** Share paths get the same treatment as every other path Farrow
touches: canonicalized, symlink-refusing, containment-checked, and rejected for
reserved system trees. A share is part of the resolved spec, so adding or
changing one is ordinary drift. Shares are never implicit — no automatic
mounting of the working directory.

**Risk.** 9p is slow for large trees and its permission mapping is awkward.
Mitigate with `mapped-xattr` security model, document the performance profile
honestly, and keep shares opt-in.

**Implementation status (2026-08-26).** The strict configuration, resolved
intent, QEMU device, inherited host-directory descriptor and guest mount path
are implemented in source. The change has deliberately not been promoted to
the verified table yet: no build or native Quick/four-node replay has been run
after this implementation.

**Acceptance.** Native round-trip on both Tier‑1 hosts: write on host, read in
guest and back; correct ownership as `dba`; survives stop/start; a share whose
host path disappears fails the node closed rather than booting without it.

### 1.2 Offline snapshot and restore

**Problem.** There is nothing between a working lab and a rebuild.

**Design.** Lean on the existing invariant that `qemu-img` runs only while the
VM is stopped — `qemu-img snapshot -c/-a/-d/-l` is already available on both
hosts.

```bash
farrow snapshot create <name>     # all nodes, project must be stopped
farrow snapshot list [--json|--yaml] [--verbose]
farrow snapshot restore <name>    # requires --force
farrow snapshot delete <name>     # requires --force
```

A snapshot covers every node's root and non-persistent data disks plus the
resolved spec hash. Restoring into a project whose spec hash has moved is
refused, not reconciled. Persistent disks are excluded by default — they are
explicitly the disks that survive lifecycle operations.

**Risk.** Internal qcow2 snapshots grow the image file and are easy to leak.
Ship `snapshot list` with sizes from day one and include snapshots in the
project GC accounting (1.5).

**Non-goal for Phase 2.** Live snapshots via QMP `savevm`. Offline-only keeps
the storage boundary intact.

### 1.3 Single-node reset

**Problem.** One broken node forces a full lab rebuild.

**Design.** `recreate` already accepts node selectors and already guards the
lease correctly. Extend the same guarded path to destroy:

```bash
farrow destroy --force meta-1        # currently exit 2
```

Reuse `internal/private/destroy.go`'s existing `allowPartialDestroy` path,
which already refuses when another project or UID owns the lease and when a
selected node is in the wrong phase. Partial destroy must additionally hold the
lease for the surviving nodes and leave their state untouched.

**Risk.** Low. The guards exist; this exposes them.

**Acceptance.** Destroy one node of a running four-node lab; peers keep
running; `status` shows the node absent; a later `up` recreates only it.

### 1.4 Recovery that tells you the next command

**Problem.** Observed verbatim during the rehearsal:

```text
private node meta-1 phase stopping requires repair before start
```

Correct, and it stops the user cold. `repair --dry-run` output, once found, was
excellent — it named the exact file and the evidence behind each action.

**Design.** Every fail-closed message that has exactly one intended remedy
names it. This is a message audit across the fail-closed paths, not a
behaviour change: a typed remedy field on the error, rendered in text and
carried in `--json`.

**Risk.** None. Do not let it turn into auto-repair — the operator's review is
the safety mechanism.

### 1.5 Project garbage collection

**Problem.** Nine destroyed projects were still listed on the rehearsal host.
`image prune` exists; the project equivalent does not.

**Design.**

```bash
farrow project prune --dry-run     # default
farrow project prune --yes
```

A project is a candidate only when its marker directory is gone or its workdir
no longer exists, no node artifacts remain, no persistent disk is retained, no
process matches, and it does not hold the lease. Same allowlist discipline as
`purge-keys`. Report reclaimed bytes, including snapshots.

**Acceptance.** Prune reclaims exactly the orphans and refuses every project
that fails any one of those conditions.

## Stage 2 — capacity and concurrency

Higher risk. Stage 2 changes a core invariant and needs its own review.

### 2.1 Resource admission control

**Problem.** Nothing checks host capacity. Declared totals across the shipped
profiles:

| Profile | Nodes | Declared RAM |
|---|---:|---:|
| `simu` | 20 | 94 GiB |
| `deci` | 10 | 60 GiB |
| `citus` | 13 | 28 GiB |
| `full` | 4 | 10 GiB |

On a 64 GiB host, `farrow up -f simu` starts twenty QEMU processes and lets the
host sort it out. Measured actual usage stays well below the declared ceiling
early on — the two-node lab held 2.0 GiB resident against 4 GiB declared — but
that is lazy allocation, not a guarantee.

**Design.** Extend `doctor` and `plan` with a memory verdict alongside the
existing data-root capacity check: declared total versus host memory, with a
configurable headroom. Over the limit, `plan` warns and `up` refuses with
exit 3 unless `--overcommit` is explicit. `virtio-balloon-pci` is available on
both hosts, so a later step can attach a balloon and reclaim guest memory; the
admission check comes first because it is the part that prevents the bad
outcome.

### 2.2 More than one lab per host

**Problem.** The single host-global lease is the most limiting product
constraint. A developer comparing two Pigsty versions cannot run both.

This invariant bought real safety and should not be discarded casually. Three
options:

| Option | Mechanism | Cost |
|---|---|---|
| **A. Keep one lab** | status quo | simplest; the constraint stands |
| **B. One subnet per lab** | network install allocates several `/24`s; lease becomes a lease set keyed by subnet | moderate: addressing, preflight, and teardown all become set operations |
| **C. One bridge per lab** | a `farrow0`, `farrow1`, … bridge or vmnet interface per lab | highest: multiplies the privileged surface on both platforms |

**Recommendation: B, with a hard cap.** Farrow already treats the subnet as the
unit of allocation — host `.1`, DHCP `.8`, node suffixes and lease all move
together. Extending the lease to a set keyed by subnet reuses that model
instead of inventing one. Cap at four concurrent labs so the privileged
install stays reviewable and the failure modes stay enumerable.

The lease record already carries UID, network, IP, MAC, QMP and process
identity, so the identity audit and the exit-6 conflict message generalize to
"this subnet is held by project X" with no change in meaning. The observed
message already names the holding project and UID.

**Risk.** This is the one change that can regress the safety story. It needs
its own design review, a soak across concurrent labs, and explicit answers for
partial install, partial teardown, and a lease set where one member is stale.

**Do not start 2.2 before Stage 1 has shipped and 1.0 is out.**

## Stage 3 — platform reach

Opportunistic. Each is independently useful and independently droppable.

### 3.1 Linux private mode on wireless hosts

Stock `80-wifi-adhoc.network` ships with systemd, so any host with a wireless
interface and dormant networkd fails install at exit 7. The refusal is correct
— starting networkd could reconfigure the link the user is connected through —
but it is the first thing most Linux desktop users hit.

Two honest improvements, neither of which weakens the proof:

- Model `WLANInterfaceType` so an ad-hoc-only file stops matching an ordinary
  station link. This removes the common false positive.
- When the proof fails, print the specific file, the specific link, and the
  three real options rather than a bare refusal.

Adopting networkd on the user's behalf stays out of scope permanently.

### 3.2 Cross-architecture guests

Pigsty ships amd64 and arm64 packages; Apple silicon developers cannot
currently test amd64 at all. Native-only is invariant 2 and it is worth
keeping, so the design is explicit rather than silent:

- a separate `arch: emulated` value, never inferred, never a fallback;
- `doctor` and `status` label such a node emulated everywhere it appears;
- excluded from the formal guest matrix and from any performance claim.

If that framing is not acceptable, the honest answer is a remote amd64 builder,
not local emulation.

### 3.3 Image mirror and freshness

All fourteen embedded URLs resolved during the rehearsal. That is luck with a
deadline: the dated upstream directories rotate. Stage 0's hosting decision
fixes provenance; this item adds the operational half — a freshness check that
reports when an embedded entry no longer resolves, and a documented refresh
procedure through the existing normalization pipeline.

## Non-goals

Stating these plainly is cheaper than relitigating them:

- Live migration, clustering, or any multi-host orchestration.
- A guest agent. vsock is unavailable on both Tier‑1 hosts; SSH plus the ready
  marker is the channel.
- A plugin or provider framework.
- Arbitrary QEMU argument passthrough.
- A global cleanup command.
- Windows hosts. WSL2 users run the Linux build inside WSL2.
- Automatic repair. The operator's review is the safety mechanism.

## Open questions for the owner

1. Is 9p performance acceptable for the Pigsty tree, or should Stage 1 start
   with a measurement before committing to the mechanism?
2. Is the one-lab constraint actually costing users today, or is it
   theoretical? 2.2 is the most expensive item here and should not be built on
   a guess.
3. Should snapshots include persistent disks behind an explicit flag, or stay
   excluded permanently?
4. Is emulated cross-architecture acceptable under an explicit opt-in label, or
   does native-only stand absolutely?

## Sequencing

| Stage | Content | Gate to start |
|---|---|---|
| 0 | image hosting, key custody, release custody, macOS runner | now; owner decisions |
| 1 | 9p shares, offline snapshots, single-node reset, remedy messages, project GC | after 1.0 ships |
| 2 | resource admission, then multi-lab | after Stage 1 ships and question 2 is answered |
| 3 | networkd matching, emulated arch, image freshness | independently, as capacity allows |

Stage 1 is five self-contained changes inside the existing invariants. Stage 2
contains the only architectural decision in this document. Stage 3 is optional.
