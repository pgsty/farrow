# Command reference

```
farrow [--json|--yaml] [--verbose] <command> [flags] [node...]
```

## Conventions

**Config discovery.** Commands that act on a project look for `-f <file>`
first, then `farrow.yaml` in the working directory. With neither, they operate
in quick mode on a single VM named `meta`.

**Node selectors.** `plan`, `up`, `start`, `stop`, `restart`, `recreate`,
`status` and `provision` accept trailing node names to limit the operation.
`destroy` does not — it always acts on the whole project.

**Output.** Text is the default. `--json` emits JSON; `--yaml` emits YAML with
the same field names and scalar types as JSON. These are global presentation
flags and may appear before or after the command, but a literal `--` ends their
parsing so remote command arguments are untouched. JSON and YAML are mutually
exclusive.

Final results and command payloads go to stdout. Progress, warnings, errors and
verbose diagnostics go to stderr, so structured stdout remains directly
parseable. ANSI styling is used only for a real terminal; redirected streams,
files, `NO_COLOR`, and `TERM=dumb` are plain. Long bounded operations report an
initial state, a restrained heartbeat after prolonged silence, and completion.

`--verbose` adds redacted implementation detail on stderr: selected mode,
resolved targets, paths, operation IDs, timeouts and phases. It never changes
the stdout schema, exit code, command behaviour, or QEMU's separate
`--log-level` setting.

Some commands produce native payloads or streams:

- `init`, `ssh-config`, `pigsty inventory`, and `completion` preserve their
  redirectable text payload in default mode; JSON/YAML returns an equivalent
  structured representation or a metadata envelope.
- `exec` captures up to 4 MiB each of remote stdout and stderr and returns them
  with exit code, duration and truncation flags in structured modes.
- interactive `ssh` keeps its live terminal session on stderr in structured
  modes, then writes node, endpoint, exit code and duration to stdout as one
  JSON/YAML result. Use `exec` when remote stdout/stderr must be captured as
  fields instead of displayed interactively.
- `logs --follow --json` is NDJSON. `logs --follow --yaml` is a YAML
  multi-document stream. A finite `logs` call remains one document and bounds
  embedded content at 4 MiB, reporting both captured and total bytes plus a
  truncation flag. Follow records are capped at 64 KiB; `continued: true`
  means the same source line continues in the next record.

**Confirmation.** Destructive operations print their plan and change nothing
until you pass `--force` (project data) or `--yes` (host state).

## Exit codes

| Code | Meaning | Typical cause |
|---:|---|---|
| 0 | success | |
| 1 | runtime error | unexpected failure |
| 2 | usage error | bad flag, invalid config |
| 3 | capability missing | no QEMU, no native accelerator, no firmware |
| 4 | state conflict | drift needs `--restart`; private project needs `recreate --force` |
| 5 | partial completion | some nodes of a multi-node operation failed |
| 6 | resource held | another project owns the private lease |
| 7 | integrity failure | checksum, signature, or ownership mismatch |

`ssh`, `exec`, and a single-node `provision` pass the remote exit code through
unchanged; 255 means SSH itself failed. A mixed multi-node provision result
returns 5.

## Lifecycle

```bash
farrow plan      [flags] [node...]   # classify the pending action, change nothing
farrow up        [flags] [node...]   # create and boot, or converge an existing project
farrow start     [flags] [node...]
farrow stop      [flags] [node...]
farrow restart   [flags] [node...]
farrow recreate  [flags] [node...]   # destroy then create; requires --force
farrow status    [flags] [node...]
farrow destroy   [flags]             # requires --force
```

Flags are accepted only where they can act. A flag passed to a command that
cannot use it is a usage error, not a silent no-op:

| Flag | Accepted by | Description |
|---|---|---|
| `--json` | global | stable JSON result |
| `--yaml` | global | YAML result with the JSON field contract |
| `--verbose` | global | bounded, redacted diagnostics on stderr |
| `--log-level <level>` | all | QEMU diagnostic level: `error`, `warn`, `info` (default), `debug` |
| `-f <file>` | `plan`, `up`, `recreate` | declarative configuration file |
| `--force` | `destroy`, `recreate` | confirm a destructive operation |
| `--delete-persistent` | `destroy` | also delete `persistent: true` data disks; requires `--force` |
| `--no-wait` | `up`, `start`, `restart`, `recreate` | return once QMP and process identity are confirmed, without waiting for guest SSH and the ready marker |
| `--restart` | `plan`, `up` | stop a running VM to apply restart-class drift |
| `--rollback` | `up` | private projects only: remove artifacts from nodes that failed during this prepare, leaving successful peers running |

Once a project exists, `start`, `stop`, `restart`, `status` and `destroy` read
the resolved spec from project state — they take no `-f`. Supplying a
configuration is how you *change* a project, which is why only `plan`, `up` and
`recreate` accept it.

Quick-mode overrides, accepted by `plan` and `up` only, and ignored when a
config file is in use:

| Flag | Default | Description |
|---|---|---|
| `--image <alias>` | `u24` | guest image |
| `--cpus <n>` | `2` | CPU count |
| `--memory <size>` | `4GiB` | memory |
| `--root-disk <size>` | `64GiB` | root disk |
| `--data-disk <size>` | `64GiB` | `/data` disk |
| `--no-data-disk` | | omit the `/data` disk |
| `--forward [bind:]host:guest` | | add a TCP forward with an IPv4 bind address; repeatable |
| `--no-default-forwards` | | omit the four business forwards |

### Drift

`up` converges a stopped VM automatically. On a **running** VM, changes to CPU,
memory, forwards, or YAML `shares` return exit 4 and do nothing — rerun with
`--restart` to apply them across a stop/start cycle. Shares have no CLI flag;
they are intentionally explicit per-node configuration.

Private projects treat any change to the desired spec hash as destructive.
`up -f` returns exit 4; review `plan`, then apply with
`recreate -f farrow.yaml --force`.

### Deletion

`destroy --force` removes node artifacts and preserves the image cache, the
project marker, project SSH keys, and every `persistent: true` data disk. A
later compatible `up` reattaches those disks.

`destroy --force --delete-persistent` is the only way to delete persistent
disks. Project keys are removed separately by `project purge-keys`.

## Inspection

```bash
farrow doctor [--json|--yaml] [--verbose]
```

Checks host tier, QEMU path and version, accelerator, machine type, CPU model,
required virtio devices, a real accelerated boot smoke, `qemu-img`, firmware,
OpenSSH, the project marker, data-root capacity, and private-network readiness.
On Linux it also reports the distribution family, `/dev/kvm` access,
systemd-networkd state, `qemu-bridge-helper` ownership and the `farrow0` bridge.
Capability probe results are cached against the exact QEMU path, size, mtime and
version.

Doctor exits 3 when any capability is missing — including private-network
readiness on a host that has never installed it. That verdict does not affect
quick mode, which needs none of those components.

```bash
farrow list [--json|--yaml] [--verbose]                    # every project on this host
farrow logs [node] [--source serial|qemu|events] [--follow]
farrow debug bundle [--output <path>] [--json|--yaml] [--verbose]
farrow repair [--dry-run|--force] [--json|--yaml] [--verbose]
```

`--source events` is the structured operation timeline — operation UUID, action,
phase, level and a redacted message. Start there when something misbehaves.

`repair` defaults to a dry run. It reconciles state after a host or CLI crash
using only ownership-bounded, path-exact actions. Read the printed actions
before passing `--force`.

`debug bundle` collects logs and metadata through a fixed allowlist with
redaction, writes mode 0600, and never overwrites. It excludes seeds, disks,
project keys and `known_hosts`. Review the printed file list before sharing.

## Configuration

```bash
farrow init [profile] [--scale 1..64] [--image <alias>] \
            [--network-cidr <RFC1918/24>] [--force-uniform-image] \
            [--json|--yaml] [--verbose]

farrow validate [-f <file>] [--json|--yaml] [--verbose]
```

`init` writes a complete strict configuration for a built-in profile to stdout;
with no profile it emits the quick single-node spec. `--scale` multiplies CPU
and memory only, and is rejected for profiles the catalog marks non-scalable.
`--image` is rejected for mixed-distribution profiles unless
`--force-uniform-image` is explicit. `--network-cidr` rebases the whole `/24`
while preserving each node's last octet.

`validate` parses strictly — unknown fields and trailing data are errors — and
prints the resolved spec hash.

See [config.md](config.md) for the file format and the profile list.

## Access

```bash
farrow ssh  [node]
farrow exec [node] -- <command> [args...]
farrow provision --script <path> [--sudo] [--parallel 1..4] \
                 [--timeout <duration>] [--json|--yaml] [--verbose] [node...]
farrow ssh-config [--install|--remove] [--name <prefix>] \
                  [--json|--yaml] [--verbose] [node...]
farrow hosts install|uninstall [--json|--yaml] [--verbose] [--yes]
```

`provision` reads one non-empty, regular, non-symlink local file (maximum
4 MiB), snapshots its bytes and SHA-256 once, then streams that snapshot to a
fixed guest command: `/bin/bash -se`, or
`sudo -n -- /bin/bash -se` with `--sudo`. It requires running nodes, defaults
to serial execution, caps concurrency at four and holds the project lifecycle
lock until every selected node finishes. The operation has a hard deadline
(default 1 hour, maximum 24 hours). It does not interpret a Vagrantfile, invoke
a host shell, add automatic `up` hooks, or persist script bodies and output in
the event log. Human and JSON results include bounded per-node stdout/stderr,
exit status and duration; the audit trail records only digest, byte count,
selection and result metadata.

`ssh-config` with no flag prints a fragment. `--install` writes a marker-owned
fragment plus an `Include` line in `~/.ssh/config`, so `ssh meta` works
directly. `--remove` removes only this project's fragment and `Include`.

`hosts` publishes the project's node aliases to `/etc/hosts` through a
root-owned helper whose digest is pinned into the CLI at build time. It prints
the exact plan and changes nothing without `--yes`.

## Images

```bash
farrow image list [--json|--yaml] [--verbose]
farrow image info [--json|--yaml] [--verbose] <alias>
farrow image pull [--json|--yaml] [--verbose] <alias>
farrow image prune [--dry-run|--yes] [--json|--yaml] [--verbose]
farrow image import [--sha256 <digest>] \
                    [--name <alias> --boot bios|uefi --source-user <user>] <path>
farrow image sync [--allow-downgrade] [--json|--yaml] [--verbose] <url|path>
farrow image reset-manifest [--json|--yaml] [--verbose]
```

`prune` defaults to listing unreferenced cache entries; `--yes` deletes them.
`sync` installs a signed manifest and refuses versions below the recorded
high-water mark unless `--allow-downgrade` is explicit. `reset-manifest`
restores the manifest embedded in the binary.

See [images.md](images.md).

## Network

Host-global state. All four subcommands operate on the whole host, not a
project.

```bash
farrow network preflight [--cidr <RFC1918/24>] [-f <file>] [--json|--yaml] [--verbose]
farrow network status    [--cidr <RFC1918/24>] [--json|--yaml] [--verbose]
farrow network install   [--cidr <RFC1918/24>] [--mode host|shared] \
                         [--archive <path>] [--interface-id <uuid>] \
                         [--json|--yaml] [--verbose] [--yes]
farrow network uninstall [--cidr <RFC1918/24>] [--mode host|shared] \
                         [--archive <path>] [--interface-id <uuid>] \
                         [--json|--yaml] [--verbose] [--yes]
```

`preflight` is read-only and runs automatically before install and before every
private lifecycle mutation. With `-f`, it probes the exact node addresses in
that config.

`--archive` and `--interface-id` are macOS-only: the pinned socket_vmnet
tarball and the persistent vmnet interface UUID. `--mode` is macOS-only and
defaults to `host`.

`install` and `uninstall` print the full privileged plan — paths, ownership,
modes, and rollback — and change nothing until `--yes`. `uninstall` refuses
while a private lease is active.

See [networking.md](networking.md).

## Project

```bash
farrow project purge-keys    [--dry-run|--yes] [--json|--yaml] [--verbose]
farrow project upgrade-state [--dry-run|--yes] [--json|--yaml] [--verbose]
```

`purge-keys` deletes the project's SSH key material. It refuses while any node
directory or retained persistent disk exists, and accepts only the fixed file
allowlist `id_ed25519`, `id_ed25519.pub`, `known_hosts`, `known_hosts.old`.

`upgrade-state` migrates project state to the current schema. It requires every
node stopped and no active private lease, writes mode-0600 backups first,
verifies them, then publishes atomically. It is idempotent, and refuses state
written by a newer binary.

Both default to a dry run.

### Upgrading the binary

1. Stop every project and confirm no private lease is held. Record
   `farrow list --json` and `farrow status --json`.
2. Verify the new release: checksums, SBOM, and signature bundle.
3. Replace the CLI. Packages also replace the root-owned hosts helper with its
   digest-paired companion.
4. In each project, run `project upgrade-state --dry-run`, then `--yes`.
5. Smoke it: `doctor`, `plan`, `start`, `exec -- true`, `stop`.

Keep migration backups through at least one successful lifecycle. Rolling back
to an older binary is only safe when it understands the retained state schema —
restore from the exact 0600 backup, with every VM stopped, after comparing
project IDs and spec hashes. Never pair runtime disks with state from a
different generation.

Host network state is separate and privileged. Upgrade it with
`network uninstall` then `network install`, never by copying files. macOS must
reuse the same persistent interface UUID.

## Pigsty

```bash
farrow pigsty inventory --profile <name> --root <absolute-path> \
                        [--scale 1..64] [--network-cidr <RFC1918/24>] \
                        [--output <absolute-path> --force]
```

Renders a Pigsty inventory bound to the same profile, scale and subnet as the
VM topology. See [pigsty.md](pigsty.md) — normally you drive this through the
`pigsty-vm` wrapper rather than calling it directly.

## Misc

```bash
farrow version
farrow completion bash|zsh|fish
```
