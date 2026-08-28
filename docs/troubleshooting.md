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

With a `farrow.yml`/`pigsty.yml` present, plain `farrow setup` prepares that
exact file, and `farrow setup <template>` is accepted only when the file
resolves to the same generated template. An invalid or different file is
never overwritten — prepare it as written with `farrow setup`, or generate
the template in an empty directory.

## `farrow doctor` reports a missing capability

`doctor` remains the detailed read-only probe. Normally rerun `farrow setup`
first; it installs supported dependencies and performs the same final host
verification. A successful source build alone proves nothing about the host.

## Native acceleration or a compatibility runtime is unavailable

Native paths require HVF on macOS and KVM on Linux. On Linux, check that
`/dev/kvm` exists and that your user can open it — usually membership in the
`kvm` group. Inside a VM, nested virtualization must be enabled by the outer
hypervisor.

TCG is selected only for an explicit foreign `vm_arch` or a built-in
image/host compatibility rule; an arbitrary native failure never falls back.
Homebrew QEMU includes both system emulators. Linux setup remains host-native,
so a foreign `vm_arch` there also requires the matching `qemu-system-*` binary
and UEFI firmware to be installed separately.

## `up` times out waiting for SSH

The VM booted but the guest never became ready. Look at the console:

```bash
farrow logs --source serial
```

Common causes: cloud-init failed on a custom image, the image does not carry
the NoCloud datasource, or a data disk mount check failed. The ready marker is
only written after disk, identity and — on private nodes — `private0` checks
all pass, so a partial boot never counts as ready.

The readiness budget is fixed at 180 s. Use `--no-wait` to return as soon as
QMP confirms the process, when you intend to check readiness yourself.

## A port is already in use

The management SSH forwards prefer `2222 + node index` and fall back to
`preferred + n × 10000` for `n = 1..4` before giving up rather than scanning
forever. The port each node got is in the resolved spec and in `status`. If
every candidate is taken, free one of them.

## QMP socket is absent

Distinguish a crashed VM from a stale runtime directory. `stop` escalates to
SIGTERM and SIGKILL only when QMP is proven unavailable *and* executable,
start time, argv hash and invocation all still match state. On mismatch it
refuses rather than signalling something else. Ambiguous state is preserved
and reported with the exact evidence that is missing — never delete disks or
edit state by hand.

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
Deployment-level changes (login user, subnet, defaults) recreate the whole
deployment. Restart-class in-place application (`vm_cpu`/`vm_mem` without a
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
SSH clients, preserve the VMs and the deployment, and report each unfinished
node as a failure. Script bodies and captured output are not copied into the
event log, so retain the command's stdout/stderr when diagnosing guest code.

## The data root holds a pre-simplification layout

```text
~/.farrow holds a pre-simplification multi-project layout; farrow now keeps
exactly one deployment there — remove it with `rm -rf ~/.farrow` (images are
re-pulled on demand) and run setup/up again
```

A data root containing the old `projects/` registry predates the
one-deployment simplification, and Farrow refuses to interpret it. There is
no in-place migration: remove the whole directory as the message says —
images are re-downloaded on demand, and pre-simplification VM state is not
salvageable by the current binary.

## Private subnet conflicts

For a newly generated `meta` or `full` profile, setup automatically tries a
bounded set of free RFC1918 `/24` alternatives. If it still stops, the conflict
is explicit or unsafe to change: a user-supplied config/CIDR, a foreign owner,
or a partial installation. Inspect the evidence:

```bash
farrow setup --dry-run --json
farrow network status --json
```

If a foreign route, interface, service, or address owns the subnet, stop that
named owner or choose a free subnet. Farrow never deletes or adopts it. For a
generated lab that has not retained data, replace the whole decision:

```bash
farrow destroy --force
farrow network uninstall --yes
mv farrow.yml farrow.yml.previous
farrow setup full --network-cidr 172.31.251.0/24
farrow up
```

Review `farrow.yml.previous` before deleting it. For a hand-maintained
config, edit every node address together as one `/24` change, then rerun
`farrow setup`. Changing only the daemon subnet or only the node IPs
produces an inconsistent host.

## `setup` finds a partial or unsafe private installation

Setup does not turn an ownership or integrity error into an automatic cleanup.
Start with the read-only record:

```bash
farrow network status --json
```

The finding identifies the exact marker, owner, mode, path, service, or
interface that failed. If it is a provably owned old Farrow installation,
stop the deployment and use the explicit `network uninstall` plan. If
ownership is ambiguous, preserve it and repair the named host component as an
administrator; do not remove paths by glob.

## macOS: setup cannot fetch socket_vmnet

Setup prefers the Homebrew formula (`brew install socket_vmnet`), which
follows your brew mirror and proxy configuration. The release download is
only the fallback, and it honors `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY`. When
github.com is unreachable, either install the formula through your brew
mirror, point `FARROW_REPO` at a mirror that serves
`<repo>/socket_vmnet/<archive>`, or drop the release tarball anywhere and
pass `FARROW_VMNET_ARCHIVE=/path/to.tar.gz` — every source is SHA-256
verified, so mirrors need not be trusted.

## macOS: the socket exists but `10.10.10.1` does not

socket_vmnet can create its Unix socket before `vmnet.framework` succeeds. If
doctor reports no host address, read `/var/log/farrow-vmnet/stderr.log`.

Error `1009` is `VMNET_SHARING_SERVICE_BUSY`: another virtualization or sharing
service holds that subnet. Common owners are VirtualBox, OrbStack and macOS
Internet Sharing. Stop the conflicting service, or move Farrow to a different
`/24`.

Farrow will not adopt a foreign interface that happens to hold `.1/24`. Install
records the exact BSD interface it created, and the readiness probes accept
only that one.

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
`network uninstall --yes` restores the recorded prestate. It refuses while
any recorded node is live, so stop or destroy the deployment first.

## A configured host share will not start

Farrow validates 9p shares before it stops an existing VM. The `host`
directory must already exist, be owned by your UID, contain no symlinked path
component, and not be group/world writable. It also cannot overlap the Farrow
data root or managed VM state.

If the error says `virtio-9p-pci` is missing, the selected QEMU package was
built without VirtFS support; install a QEMU build that provides that device.
There is no provider fallback. If the guest mount is unexpectedly read-only,
remember that `readonly` defaults to `true`; write access requires an
explicit `readonly: false` in that node's `vm_shares` entry.

## Recovering safely

There is no global cleanup command, by design. Use scoped tools:

```bash
farrow status --json
farrow destroy --force
farrow network uninstall --yes
```

Ordinary `destroy --force` preserves the deployment keys and every
`persistent: true` disk; a later compatible `up` reattaches them. Deleting
persistent data needs the separate `--delete-persistent` confirmation
alongside `--force`.

`destroy --force --purge` is the terminal one-verb disposal: persistent
disks, the keys, and the deployment state — only the image cache survives.
Every destructive command reads the global state, so it works from any
directory; deleting a lab directory loses nothing but the configuration
file.

Any unexpected entry, unsafe owner or mode, live process, remaining node
artifact or retained disk blocks the whole operation before the first
deletion.

## Filing a report

Attach the failing command's output rerun with `--verbose` (diagnostics go
to stderr), plus `farrow status --json`, `farrow doctor --json`, and the
relevant `farrow logs --source events` lines. Events are redacted — remote
command text, script bodies, and process environment are never recorded —
but review anything else you paste, especially serial logs, before sharing.
