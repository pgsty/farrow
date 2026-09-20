# Farrow UX delivery plan

Updated 2026-09-20. The current evidence and delivery table are in
[UX-AUDIT-0.8.md](UX-AUDIT-0.8.md); dated observations below remain historical.
This plan separates working-tree changes from future work.
Source checks, native VM checks, clean-host installation, and publication are
separate acceptance results. A stage is complete only for its stated scope.

The user-facing path is `install → farrow up → farrow ssh`. Prefer continuing
the same command after a recoverable failure. Keep `init` for customization,
`setup --yes` for unattended preparation, and the existing `up`/`start` split.

| Order | Delivery | Current scope | Completion evidence |
|---|---|---|---|
| 1 | Predictable entry and bounded optional work | Implemented in the working tree; verify before release | CLI/installer regression checks, real repeated `up`, unchanged VM identities |
| 2 | Install a release and reach the first SSH command | Linux clean-account source build tested; clean-host release installation pending | Native installation from exact release artifacts, then remote `true` |
| 3 | Continue after interruption and host changes | Offline prepare retry, persistent recreate and SIGINT recovery tested; host reboot and port reassignment pending | Fault injection on isolated hosts with preserved disks and successful peers |
| 4 | Reduce time spent in repeated `up` | Stage timing is implemented; optimization pending measurements | Fewer probes/writes, live readiness retained, measured time distribution |
| 5 | Complete diagnosis across setup and lifecycle | Existing warnings retained; shared operation trace pending | One actionable default result, details available before deployment exists |
| 6 | Evaluate optional single-node NAT | Conditional prototype only | Evidence that fixed-IP networking remains a major first-use obstacle |

A local four-node check on 2026-09-14 completed three warm `up` invocations
in 0.642–0.868 seconds with unchanged PIDs and all four guests ready. One verbose
sample attributed 354 ms to optional metadata refresh. These three observations
are not a performance distribution or fresh-install evidence. They make clean
installation and recovery more valuable next steps than a broad hot-path rewrite.

**Delivery 1: finish the small changes users can benefit from immediately.**

README, `up --help`, the welcome screen, and generated Homebrew caveats lead
with `up` and `ssh`. A script still prepares explicitly with `setup --yes`.
The installer checks which executable PATH actually selects, including the
case where the new directory appears later in PATH. Equivalent symlinks are
accepted; a shadowing executable is not run for diagnosis. The printed start
command names the installed binary so it works before PATH is corrected.

After the VM outcome, optional integrations use separate contexts: 2 seconds
for the SSH configuration snapshot, 5 seconds for guest metadata, and 1 second
for event-log waits. These are internal initial budgets, not new CLI flags.
Timeouts retain the VM outcome and become warnings. One expired context does
not cancel another integration; user cancellation stops further attempts.
Local filesystem calls still complete synchronously so writes are not abandoned
in a detached goroutine. Explicit integration commands keep their own semantics.

`--verbose` records wall time attributed to each active foreground phase.
Repeated byte/node updates do not restart the clock; implicit setup and nested
progress share the outer operation's clock. Time includes prompts and waits.
Normal output and JSON stdout keep their existing shape.

Acceptance: missing/shadowed/equivalent PATH scenarios, integration timeout and
cancellation, partial/no-wait/start/destroy behavior, nested timing output,
`make check`, and repeated `up --json --verbose` on an existing lab. This
delivery does not establish clean-machine installation or publish a release.

**Delivery 2: finish the actual first-use path before adding a new network mode.**

Start with native macOS arm64 and Linux amd64. Keep other platform claims tied
to their actual validation level. Exercise a clean account or disposable host,
an existing dependency installation, an upgrade, two Farrow installations in
PATH, interrupted downloads, and an explicitly selected inventory.

For each case capture the artifact digest, executable path and version, host
capabilities, command sequence, manual interventions, time until the first SSH
command succeeds, and any recovery needed. Verify the selected archive and
paired helper. After first SSH, run `up` again and compare VM UUIDs, PIDs and
disk identities. Package extraction or cross-compilation is not a native
network/virtualization test.

Keep formula generation in `packaging/render-homebrew.sh`. Once the maintained
tap target is selected, the release workflow updates that formula only from
verified release assets. Do not put a hypothetical tap address in runnable
documentation. Publish matching quick-start instructions after the application
assets are available. An unavailable clean host is an outstanding test, not a
reason to label a simulated result as installation success.

**Delivery 3: expand recovery through bounded, state-aware cases.**

| Fault | Action | Boundary |
|---|---|---|
| Interrupted transfer | Resume the partial download with existing retry policy | Verify the final artifact |
| Interrupted prepare | Reuse journal-verified completed artifacts and finish missing work | Preserve inconclusive artifacts |
| QEMU started before CLI exit | Adopt a matching process/QMP/UUID and continue readiness | Never adopt by PID alone |
| Host reboot leaves stale running state | Prove the old runtime is dead, then start the existing disks | Keep VM and disk identity |
| Owned network service inactive | Repair once and reprobe | Require matching ownership, CIDR and installation |
| VPN subnet conflict | Rebase only a fresh unused default configuration | Preserve an existing lab's subnet |
| Automatic management port occupied | Reserve a new port and update invocation/state/SSH together | Only for an inactive node; explicit user ports stay explicit |
| One node fails | Keep usable peers and target unfinished nodes | Preserve a partial exit and accurate readiness |

Sequence fault tests as interruption, reboot, owned service recovery, then
port reallocation. Reuse prepare journals, state transitions and allocator locks.
Port reallocation requires one recoverable transaction spanning the reservation,
invocation, node state and SSH artifacts. It cannot be implemented by updating
the displayed port. Test host-level faults on disposable/isolated targets.

**Delivery 4: use the timing evidence to change the hot path.**

First remove redundant probes within one operation. Existing `Up` already
reuses running nodes and avoids fetching images for nodes with reusable disks.
Classify work as create, start existing disks, or verify a running instance
before testing capabilities unrelated to that operation. Keep process/QMP
identity, network availability and actual SSH/ready checks at their appropriate
action boundaries. Do not cache a historical `ready` result as current truth.

For guest metadata, compare the actual managed content to its desired digest
within the existing SSH probe. Include the renderer version, topology and SSH
inputs; bind any host-side hint to VM UUID and generation. A missing hint is a
cache miss. Readiness still succeeds if only optional metadata inspection fails.
An old guest can synchronize once without a disk rebuild or mandatory image
upgrade. Commit a synchronized digest only after all required managed writes
succeed. Compare content before replacing files and preserve user-owned blocks.

Only after measuring the need, run required guest updates with at most four
workers inside the existing deployment lock and guest-update time budget.
Do not release the lock while remote writes still depend on its snapshot.
Measure cold creation, stopped-node start, and running-node `up` separately;
count SSH calls and metadata writes as well as elapsed time. Use at least 20
warm samples for p50/p95; first set a baseline instead of promising a universal
one-second startup.

**Delivery 5: carry diagnosis across the entire invocation.**

Every newly handled failure needs a stable code, short cause, affected scope,
and one preferred next action. Complete the shared trace after the first four
deliveries: allocate one operation ID before implicit setup, record phase
boundaries/retries/errors, and retain logs even before deployment creation.
Reuse `diagnostics.Event` and rotation; redact URL/proxy authentication, bound
subprocess diagnostic tails, and avoid logging every progress refresh.

Build retry arguments from the actual invocation, retaining file, selectors,
repository and other behavior-changing choices. Group failures by type and
relevant context instead of raw message text alone. A configuration error
points to the edit needed; a destructive change points to `plan` first.
Text, JSON and exit codes must agree. Introduce new diagnostic fields as
optional additions, without renaming the current result contract.

**Delivery 6: decide whether NAT earns a permanent product surface.**

Use the first-use evidence to decide. A prototype would support one node,
outbound traffic and localhost SSH/explicit forwarding. It would avoid
installing the privileged fixed-IP network when dependencies already exist.
Fixed-IP labs keep their semantics and do not switch modes after a failure.
Define fresh configuration/state semantics before enabling the old `user`
network compatibility path. Validate restart, forwarding conflicts, persistent
disks, teardown and old-state handling before proposing a public mode.

**Native Linux review, 2026-09-15.**

An isolated account on an existing Ubuntu/QEMU/KVM host exercised the default
u24 image download, first boot, SSH/exec, setup reuse, stop/start/restart/reload,
no-wait, partial preparation, targeted retry, read-only sharing, persistent
recreation and cancellation after QEMU starts. Existing unrelated VMs were kept.
Twenty warm single-node runs measured p50 0.593 s and p95 0.682 s with unchanged
process and disk identities. This is not a clean-host package-install result.

The working tree now removes external HTTP reachability from guest readiness,
cleans recognized uncommitted prepare artifacts on retry, fixes remote exec argv,
and normalizes new disk modes while retaining older owned disks. Doctor no
longer treats other labs' IPs as conflicts. Bootstrap failures get inspection
and rebuild guidance; default errors omit long QEMU argv, and subsecond work
stays quiet. Source checks and native retests remain distinct from publication.

Prioritize these observed gaps next:

- Writable sharing: host UID 1001 with a normal 0755 directory differs from
  guest dba UID 88; initialization fails. Design a mapping that also permits
  edits to existing nested files without recursively changing host ownership.
- DNS: the host's first resolver times out, its fallback works, but the guest's
  default QEMU DNS path fails. Direct guest queries to the working LAN DNS pass.
  Diagnose these states separately and design a verified resolver fallback.
- Cold-boot image work: snapd.seeded added 44.349 s in one first-boot trace.
- An occupied automatically chosen SSH port is safely rejected but not
  reassigned. Preserve port/state/SSH consistency when adding recovery.
- Mixed partial results should distinguish newly verified ready nodes from
  existing running nodes not rechecked; teardown should summarize its final
  outcome instead of accumulating intermediate preservation/deletion messages.

**Test-lab resilience follow-up, 2026-09-15.**

The guest completion boundary is now management SSH plus login identity. A
failure in an individual data disk, share, hosts update, lateral SSH setup, or
private-network check records a limitation and leaves independent work running.
No successful mount or filesystem is claimed for a failed disk. Hard failures
remain for unusable management access and identity/integrity ambiguity.

`up` uses the applied deployment to refresh old guest helpers and retry incomplete stages in place,
so users can adopt these fixes without replacing existing VM disks. Readiness
warnings are saved for `status` and cleared after recovery; summary output groups
common problems and gives the short `farrow up <node>` action.

Implemented since the review above:

- Per-user 9p access fixes ownership of files created in the guest. A genuinely
  unwritable share falls back to read-only without recursive host metadata edits;
  its fstab fallback survives reboot. Repeating up retries the desired mode.
- A stopped node's occupied automatic SSH port is replaced before process launch;
  port, forwarding state and argv are committed together. All starting commands
  refresh SSH aliases, including `start` and `restart`.
- Guest private interfaces and assigned addresses are restored when missing.
- Mixed results separately count running nodes whose readiness was not checked.

Still open: full host/guest UID mapping for existing project trees; DNS fallback
when the host's first resolver is broken; image cold-boot delay; host-side missing
share sources; final-state-only destroy summaries. These are separate from the
implemented guest degradation and management-port recovery.

2026-09-16 policy: no separate repair command. Reuse working test data disks;
reset unusable filesystems and disclose data loss, including persistent disks.
Do not mistake transient device/probe/backend failures for corrupt filesystems.


**2026-09-20 audit and working-tree delivery.**

[UX-AUDIT-0.8.md](UX-AUDIT-0.8.md) supersedes the open-item status above with
19 evidence-tagged tasks. Nine groups of bounded fixes/diagnostic improvements
are implemented and tested: node-scoped share failures, existing public-key
recovery, mixed partial startup, final cleanup summaries, retained-disk cleanup,
operation tracing before deployment, contextual retries/SSH aliases, control-key
limitation checks, and safe macOS share capability diagnosis.

Native macOS tests covered a 0.7-created VM, management and lateral SSH, actual
Ansible ping, missing shares/public/control keys, an occupied automatic port,
SIGINT/retry with unchanged instance identity, and final disk/key/state cleanup.
Twenty final healthy `up` runs measured p50 0.498 s and p95 0.560 s, including the
new control-key check. The old snapd cold-boot observation is not reproduced by
a 573 ms later-boot sample; no speculative image optimization was made.

`make check` and the bilingual documentation check passed. Test VMs and their
isolated data/SSH directories were removed, while the user's four VM processes
and network service remained running. Installed Homebrew remains 0.7.0.

Full macOS sharing is still blocked by QEMU's directory-descriptor access;
accurate preflight is not a sharing fix. Guest private-key reinjection, scoped
DNS fallback, full UID mapping, script preparation from applied state, and the
remaining global image-preparation coupling are separate follow-ups. Native
Linux amd64, clean-host install, and host reboot require dedicated environments.
No release, commit, push, installed-binary replacement, or website deployment was
performed. The mini pc comparison awaits its official link.
