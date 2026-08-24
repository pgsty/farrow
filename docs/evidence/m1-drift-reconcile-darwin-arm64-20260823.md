# Drift reconcile — macOS arm64 — 2026-08-23

Result class: native real E2E for restart, root growth, data-disk addition, and
data growth, plus fault-injection coverage for partial reconcile transactions.

## Transaction design

Safe `restart`, `stop`, and metadata-only reconcile changes now stage:

- the complete desired project and node state;
- a generation-aware CIDATA seed at an operation-UUID-bound owned path;
- the exact typed QEMU invocation and materialized forwards;
- idempotent root/data qcow2 growth or one new data disk.

The journal is written before the staged seed or disks change. Recovery gives a
matching live QMP/process identity first authority and otherwise rolls forward
the recorded desired intent. Disk shrink, disk removal, image/network/machine
change, symlink/unowned paths, and an unverified live PID remain hard failures.

A unit fault injection grew a data disk irreversibly and committed only
`resolved.json`, leaving the old node state and staged seed. Repair dry-run made
no changes; forced repair published the seed, committed the node generation and
typed invocation, verified consistent state, and removed the journal. A second
test applied root and data growth normally. Real `qemu-img` integration proves
grow, idempotent same-size retry, and shrink refusal.

## Native restart-class lifecycle

The stopped product project began at two CPUs and generation 1. The exercised
sequence was:

1. `plan --cpus 3` returned non-destructive action `restart`;
2. stopped `up --cpus 3` reconciled generation 2 and launched QEMU with
   `-smp 3`;
3. the first generation-2 guest readiness run exposed two real bugs: disk init
   treated an already mounted `/data` as fatal, and cloud-init regenerated SSH
   host keys for the new instance ID;
4. the waiting CLI was interrupted while the QMP-verified VM remained healthy,
   then normal `stop` succeeded;
5. disk init was made mount-idempotent and grow-aware, and user-data now sets
   `ssh_deletekeys: false`, which official cloud-init 26.1 documents as
   preserving existing host keys while still generating missing key types;
6. only the exact stale `[127.0.0.1]:2222` project known-host entry was removed
   to recover from the already-observed generation-2 rotation;
7. stopped reconcile back to two CPUs reached generation 3 in 13 seconds and
   returned guest `nproc=2`;
8. running `up --cpus 3` without policy returned exit 4 and left generation 3,
   QEMU, and the two-CPU state unchanged;
9. `up --cpus 3 --restart` safely stopped, reconciled, and returned
   `nproc=3` at generation 4;
10. `up --cpus 2 --restart` returned the project to its original resolved spec
    and `nproc=2` at generation 5, followed by a normal stop.

The guest Ed25519 host public-key SHA-256 stayed identical through generations
3, 4, and 5:

```text
17ebb93220cff7beb9a76b2517d9867d94e76486ba3a501fdae85ec10d198904
```

The project `known_hosts` SHA-256 also stayed identical:

```text
3b4b9a022283f948bec53f4e771bb3bc378d777027f1f8e0f40150a53614f768
```

## Native root/data growth lifecycle

An isolated retained project exercised irreversible growth without touching the
main product project:

```text
workspace: /Users/vonng/Library/Caches/piglet/product-e2e/disk-drift-project-recovered-6557f2b3
data:      /Users/vonng/Library/Caches/piglet/product-e2e/disk-drift-data-zh8o33
project:   6557f2b3-be51-4106-a468-9a2880d8d8ca
```

The native sequence and observed values were:

- create with 8 GiB root, no data disk, SSH-only user networking; guest root
  filesystem reported 7,203,201,024 bytes;
- stopped root plan classified `stop`; qcow2 grew exactly
  8,589,934,592→10,737,418,240 bytes with the original managed backing path;
  guest root filesystem grew to 9,283,444,736 bytes;
- stopped add-disk plan classified `stop`; Piglet created a backing-free 4 GiB
  qcow2, guest `/dev/vdb` was ext4, mounted at `/data`, block device size was
  4,294,967,296 bytes, and filesystem size was 4,143,677,440 bytes;
- stopped data-grow plan classified `stop`; qcow2 and guest block device grew
  exactly to 6,442,450,944 bytes, while online ext4 grew to 6,257,475,584
  bytes;
- the guest host-key SHA-256 remained
  `9a8a97a0352bf6aaa024a6ba3874627915eab1382a66f719f50138fc5cea1d1f`
  across all four generations;
- final root/data `qemu-img check` passed, phase is stopped, and there is no
  journal, staged seed, partial file, runtime directory, or QEMU process.

The same stopped project then added only `127.0.0.1:19000 -> guest:9000`.
Plan classified the change as non-destructive `restart`; resolved state and
typed QEMU argv contained SSH plus that one business forward. A transient guest
Python HTTP service was reachable through port 19000, `lsof` attributed the
listener to the exact QEMU PID, and the listener was absent immediately after
normal stop. Final generation is 5 with no QEMU/runtime leak.

The first harness command accidentally used the source repository as cwd. Its
marker was moved intact to the recovered workspace above before continuing.
A second create had already begun in the originally printed workspace before
the mismatch was noticed; it was interrupted before node state or QEMU existed.
To avoid unapproved deletion, these exact artifacts are retained untouched:

```text
workspace: /Users/vonng/Library/Caches/piglet/product-e2e/disk-drift-project-fGnt0C
project:   /Users/vonng/Library/Application Support/piglet/projects/2620aae6-f236-4afa-8bb1-d14cd4375afd
partial:   /Users/vonng/Library/Application Support/piglet/cache/images/sha256/.download-177787045.partial
```

Final facts:

- persisted phase `stopped`, generation 5, two CPUs, original spec hash;
- no transaction, staged seed, partial file, QEMU process, or runtime directory;
- root and data overlays both pass `qemu-img check`;
- `make check` passes unit, race, vet, Staticcheck, and four cross-builds.

Cloud-init reference used for the host-key behavior:
<https://docs.cloud-init.io/en/latest/reference/modules.html>.

Remaining FR-130 evidence is Linux/KVM execution.
