# Troubleshooting

Start by re-running the idempotent bootstrap. It fixes missing supported
dependencies and reports the first host boundary it cannot safely cross:

```bash
farrow setup                       # prepare the discovered config, or a single-node lab
farrow setup full                  # four-node lab template in an empty directory
```

Then narrow a runtime problem with:

```bash
farrow status --json               # what Farrow thinks is running
farrow logs --source events        # structured operation timeline
farrow logs --source serial        # guest console
farrow plan                        # what the next action would do
```

`--source events` records operation UUID, action, phase, level and a redacted
message for every step. It is usually faster than reading the QEMU log.

## `setup` says Homebrew is missing

Farrow uses Homebrew as the supported dependency installer on macOS, but does
not execute Homebrew's remote installation script on your behalf. Install it
from its official site, load its environment, and rerun the same command:

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
if [[ -x /opt/homebrew/bin/brew ]]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
else
  eval "$(/usr/local/bin/brew shellenv)"
fi
farrow setup
```

Farrow does not silently pipe a third-party installer into a shell. That trust
decision belongs to the host owner; after Homebrew exists, setup handles QEMU.

## `setup` cannot install a Linux dependency

Setup supports APT on Debian/Ubuntu and DNF on Fedora/RHEL-family hosts. Native
Farrow packages normally install the base dependencies before setup runs:

| Format/arch | Architecture-specific | Common |
|---|---|---|
| DEB amd64 | `qemu-system-x86`, `ovmf` | `qemu-utils`, `openssh-client`, `iproute2` |
| DEB arm64 | `qemu-system-arm`, `qemu-efi-aarch64` | `qemu-utils`, `openssh-client`, `iproute2` |
| RPM amd64 | `qemu-kvm`, `edk2-ovmf` | `qemu-img`, `openssh-clients`, `iproute` |
| RPM arm64 | `qemu-kvm`, `edk2-aarch64` | `qemu-img`, `openssh-clients`, `iproute` |

The bridge follows the active network manager: NetworkManager hosts (the
whole RHEL family, most desktops) need no networkd package at all; only a
host where neither manager is active pulls in `systemd-networkd`. Do not
substitute an unrelated QEMU binary: setup verifies the native system
emulator, image tool, UEFI firmware, and accelerator after the package
manager returns.

## `setup` preserves an existing configuration

With a `pigsty.yml`/`farrow.yaml` present, plain `farrow setup` prepares that
exact file, and `farrow setup <template>` is accepted only when the file
resolves to the same generated template. An invalid or different file is
never overwritten — prepare it as written with `farrow setup`, or generate
the template in an empty directory.

## `farrow doctor` reports a missing capability

`doctor` remains the detailed read-only probe. Normally rerun `farrow setup`
first; it installs supported dependencies and performs the same final host
verification. A successful source build alone proves nothing about the host.

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

Drift is node-granular. Hosts added to the configuration are created by
plain `up` without touching peers. A changed node returns exit 4 and `plan`
names it in its `recreate` list:

```bash
farrow plan
farrow recreate --force node-1
```

A host deleted from the configuration is only *reported* (`missing` in
plan); removing it is always the explicit `farrow destroy node-1 --force`.
Project-level changes (login user, subnet, defaults) recreate the whole
project. Restart-class in-place application (`vm_cpu`/`vm_mem` without a
rebuild) is not implemented yet — those report as recreates too.

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

`setup` reuses a healthy matching network without disturbing its lease, but it
will not replace or uninstall a network while that lease is active. Stop or
destroy the project named by `farrow list --json`, then rerun setup.

## Private subnet conflicts

For a newly generated `meta` or `full` profile, setup automatically tries a
bounded set of free RFC1918 `/24` alternatives. If it still stops, the conflict
is explicit or unsafe to change: a user-supplied config/CIDR, a foreign owner,
a partial installation, or an active lease. Inspect the evidence:

```bash
farrow setup --dry-run --json
farrow network status --json
farrow network preflight -f pigsty.yml --json
```

If a foreign route, interface, service, or address owns the subnet, stop that
named owner or choose a free subnet. Farrow never deletes or adopts it. For a
generated lab that has not retained data, replace the whole decision:

```bash
farrow destroy --force
farrow network uninstall --yes
mv pigsty.yml pigsty.yml.previous
farrow setup full --network-cidr 172.31.251.0/24
farrow up
```

Review `pigsty.yml.previous` before deleting it. For a hand-maintained
config, edit every node address together as one `/24` change, then rerun
`farrow setup`. Changing only the daemon subnet, node IPs, or lease file
produces an inconsistent host.

## `setup` finds a partial or unsafe private installation

Setup does not turn an ownership or integrity error into an automatic cleanup.
Start with the read-only record:

```bash
farrow network status --json
farrow network preflight -f pigsty.yml --json
```

The finding identifies the exact marker, owner, mode, path, service, or
interface that failed. If it is a provably owned old Farrow installation,
release any lease and use the explicit `network uninstall` plan. If ownership
is ambiguous, preserve it and repair the named host component as an
administrator; do not remove paths by glob.

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

## Linux: `doctor` reports the network is not ready

On a host that has never run `farrow setup`, doctor lists network-readiness
findings but exits **0** — a not-yet-installed network is information, not a
broken host. Capability errors (no QEMU, no accelerator, no firmware) are
what exit 3.

## Linux: `setup` refuses with "could affect link"

```text
refuse to start inactive systemd-networkd: /usr/lib/systemd/network/80-wifi-adhoc.network could affect link wlp193s0
```

Exit **7**. Setup will not start a dormant `systemd-networkd` when any existing
`.network` file could claim one of your real interfaces — activating it could
reconfigure the link you are connected through.

`80-wifi-adhoc.network` ships with systemd itself, so **every host with a WiFi
interface and dormant networkd hits this**. It is a conservative match: Farrow
does not model `WLANInterfaceType`, so any file that could match a wireless
link counts as a conflict.

This refusal only applies when **neither** NetworkManager nor
systemd-networkd is active — on a NetworkManager host Farrow uses the nmcli
backend and never starts networkd, so the hazard does not exist there. Your
options:

1. **Let NetworkManager own the host** (it usually already does on a
   desktop); rerun setup and the nmcli backend is selected automatically.
2. **Adopt systemd-networkd deliberately**, as the host administrator,
   before rerunning setup. Once networkd is active, Farrow does not need the
   activation safety proof.

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
remember that `readonly` defaults to `true`; write access requires an
explicit `readonly: false` in that node's `vm_shares` entry.

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

`destroy --force --purge` is the terminal one-verb disposal: persistent
disks, keys, the registration, and the workspace marker. For finer control:

```bash
farrow project purge-keys --dry-run
farrow project purge-keys --yes
```

A project whose directory was deleted without a destroy is still removable:
`farrow list` flags it as an orphan, and `farrow project rm <id> --force`
(or `farrow project prune --yes` for the provable ones) destroys and
deregisters it from anywhere.

Any unexpected entry, unsafe owner or mode, live process, remaining node
artifact or retained disk blocks the whole operation before the first
deletion.

## Filing a report

```bash
farrow debug bundle --output ./farrow-debug.tar.gz
```

The bundle is redacted and excludes seeds, disks, keys and `known_hosts` — but
review the printed file list before sharing it.
