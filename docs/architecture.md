# Architecture

Farrow is one Go binary driving one QEMU backend. There is no provider
framework, no plugin system, and no way to pass arbitrary arguments through to
QEMU.

## Flow

```text
CLI -> desired spec -> resolved spec -> plan -> node transaction
                                                 |-> image / disks / seed
                                                 |-> management (slirp) + private NIC
                                                 |-> QEMU process -> QMP
                                                 `-> SSH + ready marker / explicit provision
```

Each layer has one data format, and they do not mix:

| Format | Role |
|---|---|
| inventory YAML | user configuration: a Pigsty-compatible inventory, strict inside the `vm_*` namespace, opaque outside it |
| versioned JSON | resolved spec, project and node state, transaction journals, lease, catalog metadata |
| qcow2 | managed base images, root overlays, data disks |
| ISO9660 | NoCloud CIDATA seed |

A **desired spec** is what you wrote, narrowed to the Farrow namespace. A
**resolved spec** is that plus every default, chosen port, image digest and
generated identifier. Each node additionally carries its own **node hash** —
the project envelope plus exactly that node's definition — and drift is
defined per node as a change to that hash. Adding a peer therefore never
moves an existing node's identity, and editing non-`vm_*` inventory
variables never causes drift at all.

Explicit post-boot provisioning deliberately sits above the resolved-spec and
VM lifecycle layers. The CLI takes one bounded script snapshot and uses the
same verified SSH connection as `exec`, while holding the project lock for the
operation. It neither changes the spec hash nor creates a second plugin or
provider model.

## Process identity

External programs are invoked with `exec.CommandContext` and an argv slice,
never a shell string. Every operation carries a deadline. Commands printed for
humans are escaped separately from the ones actually executed.

A QEMU process is identified by a tuple, not a PID: the VM name and UUID
reported over QMP, plus the executable path, process start time and argv hash
recorded in state. A PID alone is never sufficient to signal or delete
anything.

`stop` prefers a clean QMP shutdown. It falls back to SIGTERM and finally
SIGKILL only when QMP is proven unavailable *and* the full identity tuple still
matches. An identity mismatch never signals — it reports.

## Transactions and recovery

Node creation is journalled. Each journal entry names an action from a fixed
allowlist and an absolute resource path, so a crashed CLI can be reconciled
without guessing.

Recovery follows a strict authority order. Matching QMP plus the project's own
pidfile can rebuild a missing process tuple. Without state, rollback is limited
to a valid prepare journal whose action and path pass the allowlist. Runtime
safety, directory contents, file types, containment and ownership are all
checked before the first mutation; ambiguity fails the operation.

`up --rollback` on a private project removes only artifacts listed by *this
invocation's* failed, uncommitted prepare journals. It never rolls back a
committed node or a pre-existing resource, and successful peers keep running. A
failure during rollback is reported separately and does not mask the original
error.

## Runtime paths

QMP sockets and pidfiles live under
`$XDG_RUNTIME_DIR/farrow/<project-prefix>/<node-token>` when that root is a
canonical, owner-only 0700 directory. Otherwise Farrow uses a short
UID-specific 0700 root under `/private/tmp` (macOS) or `/tmp` (Linux) — short
because the platform limits Unix socket path length, which is checked before
prepare rather than discovered at boot.

Every managed component is checked for owner, mode and symlinks. Empty Farrow
parent directories are pruned after stop.

## Storage

Downloaded and imported base images are content-addressed, carry no backing or
external data file, and become read-only once verified. Runtime roots are qcow2
overlays created with an explicit `-F qcow2`. Resizing and `qemu-img`
inspection happen only while the VM is stopped.

Data disks get deterministic 96-bit, 20-character serials. The guest mounts
them by disk ID and filesystem UUID, so device enumeration order cannot move a
mount.

Optional host shares are resolved per-node intent. Farrow opens each source as
an inherited directory descriptor and exposes it through a typed QEMU 9p
device; the user-selected host path is never interpolated into an arbitrary
QEMU argument. Shares default to read-only and are deliberately separate from
managed qcow2 storage.

## Events

Lifecycle and SSH audit events are append-only JSON Lines under each node. One
CLI operation UUID ties an operation's event lines to its creation journal.
Appends are bounded, redacted, mode 0600, symlink-refusing, file-locked and
fsynced. Remote command text and process environment are never recorded.

```bash
farrow logs --source events --follow
```

## Guest bootstrap

The seed is a pure-Go ISO9660 NoCloud CIDATA image, attached read-only and
retained so the guest generation contract stays verifiable. QEMU runs with
`-daemonize`, so VMs survive CLI exit without leaving zombies.

The guest writes a ready marker only after its own checks pass: disk identity
and mounts, the login account's numeric identity, and for private nodes the
exact `private0` state — up, correct address, no default route, no DNS.
When shares are configured, the same finalizer installs their marker-owned
`/etc/fstab` block and verifies each 9p mount and its requested access mode
before writing that marker.

## Networking backends

Every node pairs a user-mode management NIC (slirp: DHCP, DNS, default
route, egress) with a fixed-address private NIC. The private side uses
`socket_vmnet` on macOS and a root-owned `farrow0` bridge with the
distribution `qemu-bridge-helper` on Linux — created through NetworkManager
(`nmcli`) when it owns the host, or systemd-networkd units otherwise. On
macOS the QEMU `stream` netdev with probed reconnect is preferred, with a Go
dial plus `ExtraFiles` and `socket,fd=3` as the runtime fallback.

QEMU is unprivileged in every case. See [security.md](security.md).

## Dependencies

The entire runtime dependency surface:

| Module | Purpose |
|---|---|
| `github.com/diskfs/go-diskfs` | ISO9660 seed generation |
| `go.yaml.in/yaml/v3` | strict YAML parsing |
| `aead.dev/minisign` | image catalog signature verification |
| `golang.org/x/crypto` | OpenSSH public key parsing |
| `golang.org/x/sys` | bounded Unix file locks and metadata |
| `golang.org/x/term` | real-TTY confirmation for destructive operations |
| `github.com/djherbis/times` | indirect, pulled in by go-diskfs |

Everything else is the standard library. Versions, the reachable-module set and
exact upstream license bytes are pinned and verified by
`packaging/verify-licenses.sh`.

Adding a dependency requires a documented purpose, a pinned version, license
review, and an explicit assessment of the standard-library alternative.
