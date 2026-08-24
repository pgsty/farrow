# M2 private full product lifecycle — Linux amd64 — 2026-08-24

Result class: **native real product E2E** on the Linux amd64 Tier-1 host. The
exact migrated `profiles/full.yaml` completed public network install, four-node
create/boot, fixed-IP and dual-NIC validation, host/NAT/lateral SSH, lease
contention, scoped crash repair, a 30-cycle soak, final stopped disk checks,
hosts/SSH integration roundtrips, and public network uninstall with clean-host
restoration.

One real SSH-config bug and one real soak-script bug were found, preserved, fixed
in the shared source by the parent agent, and rerun successfully. Audit-command
errors and one stopped-project precondition error are also retained rather than
hidden.

## Runner, source snapshot, and retained root

- Host: `vonng-aimax`, Ubuntu 24.04, kernel
  `6.17.0-35-generic`, x86_64.
- Invoking account: UID/GID 1000 `vonng`, member of `kvm`; `/dev/kvm` was
  readable and writable.
- QEMU/qemu-img: Ubuntu QEMU 8.2.2; machine `q35`, accelerator `kvm`, CPU
  `host`, OVMF `/usr/share/OVMF/OVMF_CODE_4M.fd`.
- Primary evidence window: `2026-08-23T18:47:44Z` through
  `2026-08-23T19:05:53Z` (`2026-08-24 02:47–03:05` Asia/Shanghai).
- Retained root:
  `/data/piglet-v1-full-product-linux-20260824-1kUKd3`, mode 0700,
  owner `vonng:vonng`, final size 2.2 GiB.

The initial build took byte-identical source snapshots before and after
compilation. Go 1.26.7 produced static Linux amd64 binaries with `-trimpath`.
The helper was built first; its digest was injected into the CLI through
`hostconfig.ExpectedHelperSHA256`, making the pair fail closed:

```text
piglet
  72d57b9cdac56c80893a06f9d8ad6744fb0d224cd9aa66386e65303730f15f47
piglet-hosts-helper
  c6dcb20302efaa2393c1912059d30cb836bf140f4d4fc5d06ee4ee18e0731628
full.yaml
  373ade3b60b88a7742a467d9870d734552abcd0a0ad9087aa0624b6d1efb308d
initial private-soak.sh
  a5f1520fe5c290967399306b5d9a53f03297855df6d0d0261f39f3544ad6ebce
```

The repository has no commit, so build metadata truthfully records a modified
development tree. This evidence is bound to the exact binary and input hashes,
not an invented revision.

The `/data` parent is root-owned 0755. `sudo -n` was used only to create and
chown the unique evidence root and for the reviewed public network/hosts
privileged boundaries. All QEMU processes and normal lifecycle commands ran as
UID 1000. No existing project or `/data/pgsty/piglet` content was used or
modified.

## Clean host prestate and public network installation

The preflight recorded:

- no QEMU process, `piglet0`, `10.10.10.0/24` route/address, active lease, or
  `/run/piglet`;
- no Piglet networkd/NM/qemu config, `/var/lib/piglet`, `/opt/piglet`, hosts
  helper, or hosts lock;
- `systemd-networkd` service/socket/generator/wait-online all
  disabled/inactive;
- NetworkManager enabled/active;
- `/usr/lib/qemu/qemu-bridge-helper` root:root 0755 with no
  `dpkg-statoverride`;
- `/etc/hosts` root:root 0644, no Piglet marker, no xattrs or inode flags, and
  SHA-256
  `75cf68408684c043bcd2054f4b4d0c2752f71c8492471726740dd9603040c952`.

As expected, `piglet network status --json` returned capability exit 3 in this
clean prestate. Public dry-run then showed the complete ordered plan: NM
unmanaged first, persistent networkd bridge, marker-only bridge permission,
reversible helper override, sticky lease root/lock, non-root attach smoke, and
networkd enable only after attach verification.

```bash
PIGLET_DATA_HOME="$R/data" "$P" network install --json
PIGLET_DATA_HOME="$R/data" "$P" network install --yes --json
```

Both returned exit 0. Applied verification proved:

- `piglet0=10.10.10.1/24` and route `10.10.10.0/24`;
- bridge helper root:kvm 4750 with the exact dpkg override;
- all planned root-owned files matched their reviewed SHA-256/modes;
- lease root available and inactive;
- networkd active/enabled while NetworkManager stayed active and reported
  `piglet0` unmanaged;
- the installer's non-root QEMU/QMP bridge-helper attach smoke left no QEMU or
  tap behind.

## Exact full profile and four-node create

Strict validation and read-only plan resolved source hash
`d9dc29ebfea5908d3d368b5b0724035cd098a4eef951072a1ebc5e9eb920fbe2`:

- `ssh.user=vagrant`;
- `meta=10.10.10.10`, 2 CPU, 4 GiB memory;
- `node-1..3=10.10.10.11..13`, 1 CPU, 2 GiB each;
- every node: 64 GiB root plus one 128 GiB `/data` disk;
- only `meta` is control.

The plan was non-destructive `create` with no active lease. This exact command
completed in 29 seconds with exit 0:

```bash
PIGLET_DATA_HOME="$R/data" "$P" up -f "$R/inputs/full.yaml" --json
```

Project `c3f036ec-16e2-4097-b297-d4e2f86d50f0` reached four-node `running`.
NAT SSH ports were 2222–2225. Four UID-1000 QEMU processes used KVM, user-mode
management NICs, and private bridge-helper NICs. Four tap members attached to
`piglet0`. The active host-global lease contained the exact project, owner,
addresses, dual MACs, VM UUIDs, QMP paths, invocations, and process identities.

The embedded u24 amd64 artifact was verified and published mode 0444:

```text
b3064efb500d71d6ccbe619b1716062b803e285116e040627b430aaee14cced6
artifact bytes: 264,306,688
virtual size: 3,758,096,384
```

## Guest, network, disk, and SSH contract

For each node, public NAT `piglet exec` verified:

- `x86_64`, login `vagrant` UID 1000, the exact profile CPU/memory;
- `mgmt0=10.0.2.15/24` with the sole default route and DNS/egress;
- `private0=10.10.10.10..13/24`, UP, with only the private subnet route;
- route to `10.10.10.1` on `private0`, route to `1.1.1.1` on `mgmt0`;
- no private default route and `resolvectl dns private0` had no server;
- root block device exactly 68,719,476,736 bytes and grown root filesystem
  above 60 GiB;
- data block device exactly 137,438,953,472 bytes, mounted at `/data` by
  filesystem UUID with `nofail`;
- generation-1 ready marker matching project/node/spec hash;
- outbound HTTPS status 200;
- one node-specific `/data/piglet-full-canary` persisted for later gates.

Host ICMP to all four fixed IPs passed with zero loss. Direct host SSH to each
private IP as `vagrant` passed using the project key, independently of NAT.
Public `piglet exec` covered all four NAT endpoints. The control guest's
private key was vagrant:vagrant 0600; the other three guests had no control
private key. `meta` successfully used its generated SSH configuration to reach
`node-1`, `node-2`, and `node-3` laterally over the private network.

## Lease contention

A second workspace under the same isolated root attempted the same full
profile while the first lease was active. It returned the specified exit 6 and
named the holder project/UID. The contender created only its scoped project
marker/key directory; it created no node directory, disk, QEMU, tap, or lease.
The original four QEMU processes were unaffected.

## Digest-bound hosts integration

Preflight proved `/opt/piglet` and `/etc/.piglet-hosts.lock` absent. The staged
helper was installed to the fixed path as root:root 0555 only after its digest
was verified; the packaged CLI required the same digest.

Public hosts dry-run/apply/repeat generated one marker block containing exactly
the four fixed IP/name pairs. The first apply was atomic; the repeat reported
`changed=false`. `getent ahostsv4` resolved all four names correctly. Public
uninstall dry-run/apply restored `/etc/hosts` byte-for-byte to the original
SHA-256 and preserved root:root 0644, link count, empty xattr set, and Linux
inode flags. No marker remained.

The design deliberately retains the root-owned mode-0600 hosts lock. At final
cleanup, after verifying exact `/etc/hosts` restoration, helper digest,
owner/mode/link count, lock owner/mode/size/link count, and that the two
`/opt/piglet` directories contained only the new helper path, the run used
exact `unlink` and empty-directory `rmdir` operations. The helper, lock, and
both newly created directories are absent in the final audit.

## SSH-config failure, fix, and rerun

The first multi-node SSH-config roundtrip used a temporary HOME whose existing
mode-0600 config ended with:

```text
Host user-canary
  HostName 192.0.2.1
```

The original CLI appended its marker/Include after that stanza. OpenSSH treated
the Include as conditional on `user-canary`; all `full-e2e-*` aliases therefore
failed resolution. Install and repeat were otherwise idempotent, and remove
restored the canary config byte-for-byte. The failure artifacts are retained.

The parent agent fixed the shared source to place or migrate the owned Include
in global scope before the first `Host`/`Match` stanza. A fresh stable-source
Linux build, used only for this integration rerun, has SHA-256:

```text
5a67a782791f9980e0d60c463fa94e2b79a4e0ea46c716b2fedc397b78b1abd7
```

With the fix, all twelve full-prefixed/node/IP aliases resolved through
`ssh -G` to `vagrant@127.0.0.1:2222..2225`; four actual alias SSH connections
passed. Remove again restored the user canary byte-for-byte and removed only
the owned fragment. The primary VM/network/soak lifecycle continued to use the
original `72d57b...` binary, so the mid-run rebuild is narrowly identified.

## Scoped SIGKILL and private repair

Node `node-2` was selected only after its persisted QMP/VM UUID, executable,
argv hash, UID, PID, and project-contained disk/runtime paths were verified.
The scoped target PID was 846370. `kill -KILL` terminated that one QEMU; meta,
node-1, and node-3 remained live with their original PIDs.

Public `repair --dry-run --json` returned exit 0 with exactly:

1. update node-2 state to stopped because QMP and process identity proved it
   dead;
2. remove node-2's bounded stale runtime;
3. synchronize the private lease.

Node state and lease hashes were unchanged by dry-run. `repair --force` applied
those three actions. Status then showed only node-2 stopped/inactive. Public
`start --json` restarted only node-2 with PID 920908; the other three PIDs,
UUIDs, executables, and argv hashes were byte-identical to pre-crash state.
Four taps/IPs returned, and node-2's data canary hash remained unchanged.

The audit wrapper itself failed after the real SIGKILL because an outer shell
misquoted `IFS=$'\t'` in its survivor-report loop. The target identity,
SIGKILL, and partial post-kill facts had already been saved; a separate
read-only check proved the intended one-dead/three-live state before repair.
No second signal was sent.

## Thirty-cycle full soak

The initial checked-in soak script stopped all four nodes correctly but then
reported a false QEMU residue in cycle 1. Its `ps | awk` predicate matched the
awk process's own command line because that line contained both the literal
`qemu-system` pattern and project root. Status, process, tap, lease, and runtime
audits proved the project was cleanly stopped. The failure remains under
`artifacts/soak-30`.

The parent agent fixed the predicate to require the first `comm` field to begin
with `qemu-system`. The fixed script was syntax-checked, transferred without
overwriting the original, and has SHA-256:

```text
b64ef2c273d1056452b4d8d93557ddd6c86568bf76b5402d19d005152e61c3c2
```

The first fixed-script invocation was made while the prior failure had already
left the project stopped and lease-free. Its initial `stop` exposed that private
stop is not idempotent in this state: it returned exit 1 while trying to mirror
stopping nodes into a missing lease. This precondition failure is retained in
`artifacts/soak-30-fixed`; no VM or disk was damaged. Public `start` restored
the documented running/active-lease precondition and all four canaries.

The final fresh run under `artifacts/soak-30-final` passed all 30 cycles from
`2026-08-23T18:57:30Z` to `19:04:12Z`:

- 30/30 stop JSON results: four stopped/inactive nodes;
- after each stop: no project QEMU and inactive lease;
- 30/30 starts: four running nodes with positive PIDs;
- every cycle: all four data canaries and all four private IPs reachable;
- zero stderr;
- stop average 4.00 seconds (3–9), start average 7.57 seconds (7–11).

The soak's own `SHA256SUMS` digest is
`de986069457c132fb9194410ab73a8c5f1b2b813b7a51dab4c5515cf5baaf3d0`.

## Final stop, eight disks, and host restoration

After soak, public final stop returned four stopped/inactive nodes and released
the lease. There was no QEMU, tap member, project runtime directory,
transaction, or partial file. While stopped, all eight project disks passed
`qemu-img check -f qcow2`:

- four 64 GiB root overlays, each with explicit qcow2 backing format and the
  same verified mode-0444 `b306...` cache base;
- four standalone 128 GiB data qcow2 files.

Public network uninstall dry-run listed only the reviewed Piglet files,
directories, bridge, reversible override, and recorded networkd states. Apply
returned exit 0 and `restored`. The final host audit matched the original
prestate:

- no QEMU, `piglet0`, tap, route/address, runtime, lease, partial, or hosts
  staging file;
- no Piglet networkd/NM/qemu/tmpfiles config, `/etc/qemu`, `/var/lib/piglet`,
  `/run/piglet`, `/opt/piglet`, or hosts lock;
- bridge helper root:root 0755 with no dpkg override;
- four networkd units disabled/inactive;
- NetworkManager enabled/active;
- `/etc/hosts` exact original bytes/metadata and no Piglet marker.

Post-uninstall `network status` returned capability exit 3, truthfully
describing the clean uninstalled private network. The retained primary project
has four stopped node states and all eight disks. The contender project has no
nodes. Both key directories are mode 0700 and private keys mode 0600. No project
or key was destroyed.

## Evidence boundary and replay artifacts

The following non-product audit errors are retained:

- guest audit assumed interface `management0`; actual contract uses `mgmt0`;
- non-root `blockdev` was used before retrying with guest sudo;
- control-lateral precheck assumed minimal u24 contained `ping`;
- the post-SIGKILL survivor report had the shell quoting error described above;
- one later statistics-only awk command was misquoted and replaced by a
  read-only Python TSV calculation.

The real product observations are:

- the original conditional SSH Include bug, fixed and rerun in this session;
- private `stop` on an already-stopped project with no lease returns an error
  rather than an idempotent stopped result.

This is one host/image/profile execution, not a claim for other guests, Linux
families, architectures, or reboot persistence. The image entry remains a
testing artifact rather than a signed GA image. Manual installation of the
hosts helper proves its digest-bound runtime contract, not package-manager
installation.

The retained evidence root is mode 0700 and contains generated private keys;
it must be treated as sensitive. Important artifacts include network
install/uninstall plans and reports, full resolved/state JSON, per-node guest
contracts, lease conflict, host/lateral SSH, hosts and SSH-config roundtrips,
crash/repair records, all three soak attempts, final eight-disk output, and the
final host audit.

`artifacts/EVIDENCE_SHA256SUMS` covers 386 retained input/log/artifact files and
has SHA-256:

```text
91887adbb7594446e00c173f8bebcbd54580d733ad1f3769d616178638987d66
```

