# Architecture

Piglet is one Go program that directly manages one QEMU backend. Lima is a
reference and test oracle, not a runtime dependency. There is no provider or
plugin framework and no arbitrary QEMU argument escape hatch.

## Stable boundaries

```text
CLI -> desired/resolved spec -> plan -> node transaction
                                    |-> image/disk/seed
                                    |-> user or private network
                                    |-> QEMU process -> QMP
                                    `-> SSH/readiness

versioned JSON: resolved spec, state, transaction, lease, manifest metadata
strict YAML:    user config and profiles
qcow2:          managed base, root overlays, and data disks
ISO9660:        NoCloud CIDATA
```

The M0 slice deliberately crosses the whole quick path before general APIs are
extracted:

1. Resolve the native host and QEMU capability.
2. Validate a managed qcow2 base and create an explicit-format overlay.
3. Build a NoCloud CIDATA seed with a pure-Go ISO9660 implementation.
4. Build a headless native-accelerated QEMU argv with user NAT and loopback
   forwards.
5. Start QEMU as the invoking user, identify it through QMP, wait for SSH and a
   generation-matching marker, then exercise stop/start.

## Process boundary

External programs are called with `exec.CommandContext` and argv slices. Each
operation has a deadline. Human-readable display commands are escaped
separately and never passed through a shell.

QEMU identity is the tuple of stable VM UUID/name from QMP plus executable,
process start time, and argv hash from state. A PID alone is never sufficient
for signalling or deletion.

Recovery follows the same authority order. Matching QMP plus the exact project
pidfile can reconstruct a missing process tuple after a CLI crash. Without
state, rollback is limited to a valid prepare journal whose action name and
absolute resource path match a fixed allowlist. Runtime safety, directory
contents, file types, containment, and ownership are all preflighted before the
first mutation; ambiguity is a hard failure.

For multi-node create, optional `up --rollback` removes only artifacts listed
by this invocation's failed, uncommitted prepare journals. It never rolls back
a committed node or a pre-transaction resource; successful peers remain
running. A rollback failure preserves the original typed partial error and is
reported separately.

Lifecycle and SSH audit events are append-only JSON Lines under each stable
node. One CLI operation UUID is shared by its event lines and creation journal.
Appends are bounded, redacted, mode 0600, symlink-refusing, file-locked, and
fsynced; remote command text and process environment are never event fields.

ADR-0005 selects QEMU `-daemonize` with a read-only seed retained for the
generation contract. Native lifecycle evidence confirms the VM survives CLI
exit without zombies and supports both macOS stream/reconnect and `ExtraFiles`
FD networking.

QMP sockets and pidfiles use
`$XDG_RUNTIME_DIR/piglet/<project-prefix>/<node-token>` when the XDG root is a
canonical owner-only 0700 directory. Without it, Piglet uses a short
UID-specific 0700 root under `/private/tmp` on Darwin or `/tmp` on Linux. Every
managed component is owner/mode/symlink checked, the platform socket-length
limit is enforced before prepare, and empty Piglet parents are pruned after
stop. Exact legacy flat `/tmp` paths remain accepted for already-persisted
pre-v1 state only.

## Storage boundary

Downloaded or imported bases are content-addressed, have no backing/data file,
and are read-only after verification. Runtime roots are qcow2 overlays created
with an explicit `-F qcow2`; resize and `qemu-img` inspection only occur while
the VM is stopped. Data disks use deterministic 96-bit/20-character serials;
guest mounts use by-id discovery and filesystem UUIDs.

## Dependency policy

The current dependency surface is the standard library, `go-diskfs` v1.9.4
for ISO9660, `go.yaml.in/yaml/v3` v3.0.5 for strict user YAML, `aead.dev/minisign`
v0.3.0 for format-compatible manifest verification, `x/crypto` for OpenSSH
public-key parsing, `x/sys` for bounded Unix file locks/metadata operations,
and `x/term` for real-TTY destructive-operation confirmation.
Versions, reachable-module checks, and exact upstream license bytes are pinned
in the dependency inventory and `third_party/licenses/`.
These replace external ISO tools and ad-hoc YAML/signature parsing without
introducing the Lima module. Any later dependency must have a documented
purpose, pinned version, license, and standard-library alternative assessment.

No predecessor orchestrator or Lima source has been copied into Piglet. The
embedded profile YAML and catalog are now Piglet-owned; dated migration
evidence is provenance, not a build, test, or runtime dependency.

## Pigsty integration boundary

Catalog schema 3 binds each VM profile to one Pigsty inventory template and an
explicit direct/build-subset/unused-node policy. `pigsty-vm` resolves the VM
and inventory from the same profile, scale, native architecture, and global
`/24`. The inventory path is a narrow YAML-AST transformer, not a generic
Pigsty installer: it recognizes a fixed address grammar, validates VM/host/VIP
relationships, applies resource tuning, and atomically publishes mode-0600
managed output. Pigsty source templates remain immutable inputs.

The official guest identity is also resolved before Ansible: cloud-init
creates `dba` UID 88 with primary `admin` GID 88 and gates readiness on the
numeric identity. This keeps account creation outside the live provisioning
session and makes the Pigsty node-admin task idempotent.
