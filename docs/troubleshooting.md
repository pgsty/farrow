# Troubleshooting

Start here:

```bash
farrow doctor                      # host capability
farrow status --json               # what Farrow thinks is running
farrow logs --source events        # structured operation timeline
farrow logs --source serial        # guest console
farrow plan                        # what the next action would do
```

`--source events` records operation UUID, action, phase, level and a redacted
message for every step. It is usually faster than reading the QEMU log.

## `farrow doctor` reports a missing tool

```bash
brew install qemu                                       # macOS
sudo apt install qemu-system-x86 qemu-utils openssh-client iproute2
sudo dnf install qemu-kvm qemu-img openssh-clients iproute
```

Doctor is the authority on whether a host can run Farrow. A successful build
proves nothing about the machine you are on.

## No native accelerator

Farrow requires HVF on macOS and KVM on Linux, on the native architecture, and
will not fall back to TCG. On Linux, check that `/dev/kvm` exists and that your
user can open it — usually membership in the `kvm` group. Inside a VM, nested
virtualization must be enabled by the outer hypervisor.

Cross-architecture guests are not supported. `arch: native` is the only
accepted value.

## `up` times out waiting for SSH

The VM booted but the guest never became ready. Look at the console:

```bash
farrow logs --source serial
```

Common causes: cloud-init failed on a custom image, the image does not carry
the NoCloud datasource, or a data disk mount check failed. The ready marker is
only written after disk, identity and — on private nodes — `private0` checks
all pass, so a partial boot never counts as ready.

Raise the budget with `ssh.wait_timeout` if a slow host genuinely needs more
than the 180 s default. Use `--no-wait` to return as soon as QMP confirms the
process, when you intend to check readiness yourself.

## A port is already in use

Farrow tries `preferred + n × 10000` for `n = 1..4` and then gives up rather
than scanning forever. The port it chose is in the resolved spec and in
`status`. If all candidates are taken, free one or pass an explicit
`--forward`.

## QMP socket is absent

Distinguish a crashed VM from a stale runtime directory. `stop` escalates to
SIGTERM and SIGKILL only when QMP is proven unavailable *and* executable,
start time, argv hash and invocation all still match state. On mismatch it
refuses rather than signalling something else.

```bash
farrow repair --dry-run     # read the scoped actions
farrow repair --force       # only after reading them
```

Never delete disks or edit state by hand.

A node can also strand in a transitional phase — typically after a stop that
was interrupted, or a stop and restart issued back to back:

```text
private node meta-1 phase stopping requires repair before start
```

`repair` is the intended exit. Its dry run names the exact file and the
evidence behind each action, for example *"QMP and process identity prove the
VM is dead"*, and applying it also re-synchronizes the private lease.

If prepare reports an unsafe or overlong runtime path, check
`XDG_RUNTIME_DIR`: it must be a canonical absolute directory owned by you with
mode 0700. Unset it to use Farrow's short UID-isolated fallback. Do not point
it at `/tmp`, your home directory, or anything shared.

## Changes are ignored, or exit 4

Run `farrow plan` first — it classifies the pending action without touching
anything.

On a running quick VM, CPU, memory and forward changes return exit 4 and do
nothing. Apply them with `farrow up --restart`. Stopped VMs converge on plain
`up`.

Private projects classify *any* desired spec change as destructive. `up -f`
returns exit 4; the only apply path is:

```bash
farrow plan -f farrow.yaml
farrow recreate -f farrow.yaml --force
```

Persistent disk compatibility is checked before anything is stopped.

## `provision` fails or returns exit 5

Provisioning never starts a stopped node. Confirm the selected nodes are
QMP/process-verified running with `farrow status --json`, then inspect the
per-node result from `farrow provision ... --json`. Exit 5 means some nodes
succeeded and some failed; rerun only the failed node names after fixing the
guest-side cause.

A script must be a non-empty regular local file, not a symlink, and at most
4 MiB. `--sudo` is non-interactive: a guest that does not allow the resolved
SSH user to run `sudo -n` fails instead of prompting. Timeouts terminate the
SSH clients, preserve the VM/project, and report each unfinished node as a
failure. Script bodies and captured output are not copied into the event log,
so retain the command's stdout/stderr when diagnosing guest code.

## Exit 6: the private lease is held

One private project per host. Find and release the other one:

```bash
farrow list --json
```

If the lease is stale — the owning process is gone — `repair --dry-run` shows
what can be reclaimed. A lease owned by a different UID is not reclaimable
without that user's cooperation; this is deliberate.

## Private subnet conflicts

```bash
farrow network preflight -f farrow.yaml --json
```

Preflight fails when a route, interface or address overlaps. If something in
`.9`–`.254` already answers SSH while no Farrow lease is active, `plan` and
`up` stop before creating state and name the address. Stop the other VM
runtime, or move the whole lab:

```bash
farrow destroy --force
farrow network uninstall --yes
farrow init full --network-cidr 172.31.251.0/24 >farrow.yaml
farrow network preflight -f farrow.yaml
farrow network install --cidr 172.31.251.0/24 --yes
```

Changing only the daemon subnet, only the node IPs, or only the lease file
produces an inconsistent host. Move all of them together.

## macOS: the socket exists but `10.10.10.1` does not

socket_vmnet can create its Unix socket before `vmnet.framework` succeeds. If
doctor reports no host address, read `/var/log/farrow-vmnet/stderr.log`.

Error `1009` is `VMNET_SHARING_SERVICE_BUSY`: another virtualization or sharing
service holds that subnet. Common owners are VirtualBox, OrbStack and macOS
Internet Sharing. Stop the conflicting service, or move Farrow to a different
`/24`.

Farrow will not adopt a foreign interface that happens to hold `.1/24`. Install
records the exact BSD interface it created, and preflight accepts only that
one.

## Linux: `doctor` exits 3 but quick mode works

On a host that has never installed the private network, `doctor` and
`network status` report:

```text
[error] linux-networkd: systemd-networkd is not active
[warn]  bridge-helper: ... requires reversible dpkg-statoverride
[warn]  network-installation.absent: private network backend is not installed
```

and exit **3**. This is the private-network readiness verdict, not a verdict on
your host overall. Quick mode needs none of it and works normally.
`network preflight` exits 0 in the same situation, because the subnet itself is
eligible.

Most desktop and server Ubuntu installs use NetworkManager with
systemd-networkd dormant, so this is the expected state until you deliberately
install the private network.

## Linux: `network install` refuses with "could affect link"

```text
refuse to start inactive systemd-networkd: /usr/lib/systemd/network/80-wifi-adhoc.network could affect link wlp193s0
```

Exit **7**. Farrow will not start a dormant `systemd-networkd` when any existing
`.network` file could claim one of your real interfaces — activating it could
reconfigure the link you are connected through.

`80-wifi-adhoc.network` ships with systemd itself, so **every host with a WiFi
interface and dormant networkd hits this**. It is a conservative match: Farrow
does not model `WLANInterfaceType`, so any file that could match a wireless
link counts as a conflict.

Your options, in order of preference:

1. **Use quick mode.** It needs no bridge, no networkd and no privilege.
2. **Adopt systemd-networkd deliberately**, as the host administrator, before
   installing. Once networkd is already active, Farrow does not need the
   activation safety proof and install proceeds. Understand what this does to a
   NetworkManager-managed desktop before you run it.
3. **Run the private lab on a host without the conflicting configuration** — a
   headless server with no wireless interface typically has none.

The same refusal covers a `.netdev` file that could create a virtual link, an
ambiguous match pattern, and wrong `qemu-bridge-helper` ownership. In every
case the dry plan prints the exact prior state it recorded and changes nothing.

## Linux: recovering an installed bridge

If install ran and left the host in an unexpected state,
`network uninstall --yes` restores the recorded prestate. It refuses while a
lease is active, so destroy the project first.

## A configured host share will not start

Farrow validates 9p shares before it stops an existing VM or changes the
private lease. The `host` directory must already exist, be owned by your UID,
contain no symlinked path component, and not be group/world writable. It also
cannot overlap the project marker, Farrow data root, or managed VM state.

If the error says `virtio-9p-pci` is missing, the selected QEMU package was
built without VirtFS support; install a QEMU build that provides that device.
There is no provider fallback. If the guest mount is unexpectedly read-only,
remember that `readonly` defaults to `true`; write access requires an explicit
`readonly: false` in that node's `shares` entry.

## Recovering safely

There is no global cleanup command, by design. Use scoped tools:

```bash
farrow status --json
farrow repair --dry-run
farrow destroy --force
farrow network uninstall --yes
```

Ordinary `destroy --force` preserves project keys and every `persistent: true`
disk; a later compatible `up` reattaches them. Deleting persistent data needs
the separate `--delete-persistent` confirmation alongside `--force`.

Once every node artifact and retained disk is gone:

```bash
farrow project purge-keys --dry-run
farrow project purge-keys --yes
```

Any unexpected entry, unsafe owner or mode, live process, remaining node
artifact or retained disk blocks the whole operation before the first
deletion.

## Filing a report

```bash
farrow debug bundle --output ./farrow-debug.tar.gz
```

The bundle is redacted and excludes seeds, disks, keys and `known_hosts` — but
review the printed file list before sharing it.
