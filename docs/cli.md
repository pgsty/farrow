# Command reference

```
farrow [--json|--yaml] [--verbose] <command> [flags] [node...]
```

## Conventions

**One deployment.** Farrow manages exactly one deployment per user; all state
lives under the data root (`FARROW_HOME`, default `~/.farrow`), and nothing is
written into the working directory. `status`, `start`, `stop`, `restart`,
`ssh`, `exec`, `logs`, `destroy`, `recreate`, `provision`, `ss`, `ssh-config`,
and `hosts` read that state and work from any directory. `up`, `plan`,
`reload`, `validate`, and `setup` read the configuration in front of you;
pointing them at a different file proposes a new desired state for the same
deployment, diffed node by node.

**Config discovery.** Commands that accept a configuration look for
`-f <file>` first, then `farrow.yml`, `farrow.yaml`, `pigsty.yml`, and
`pigsty.yaml` in the working directory. The content is always a
Pigsty-compatible inventory; see [config.md](config.md).

**Node selectors.** `plan`, `up`, `start`, `stop`, `restart`, `reload`,
`recreate`, `status`, `provision`, and `destroy` accept trailing node names to
limit the operation. `destroy` with selectors removes those nodes from the
deployment; without selectors it destroys the whole deployment.

**Output.** Text is the default. `--json` emits JSON; `--yaml` emits YAML with
the same field contract. Final results go to stdout; progress, warnings, and
`--verbose` diagnostics go to stderr, so structured stdout stays parseable.
ANSI styling appears only on a real terminal. `FARROW_OUTPUT` and
`FARROW_VERBOSE` set process-level defaults; explicit flags win.

Some commands produce native payloads or streams: `init`, `ssh-config`, and
`completion` keep redirectable text payloads in default mode; `exec` captures
bounded remote stdout/stderr as fields in structured modes; `logs --follow
--json` is NDJSON.

**Confirmation.** Destructive operations print their plan and change nothing
until `--force` (deployment data) or `--yes` (host state). `setup` prints one
compact transaction and asks once in a terminal; `--yes` covers automation.

## Exit codes

| Code | Meaning | Typical cause |
|---:|---|---|
| 0 | success | |
| 1 | runtime error | unexpected failure |
| 2 | usage error | bad flag, invalid config |
| 3 | capability missing | no QEMU, no native accelerator, no firmware |
| 4 | state conflict | per-node changes need `recreate --force <node>`; removed nodes need explicit destroy; no configuration found; a foreign vmnet consumer holds the subnet |
| 5 | partial completion | some nodes of a multi-node operation failed |
| 7 | integrity failure | checksum, signature, or ownership mismatch |

`ssh` and `exec` pass the remote exit code through.

## First-time setup

```bash
farrow setup [meta|dual|trio|full] [-f <file>] [--network-cidr <RFC1918/24>] \
             [--mode host|shared] [--dry-run|--yes]
```

With no template and no discovered configuration, setup generates the
single-node `meta` lab as `./farrow.yml`. With a configuration present (or
`-f`), it prepares exactly that file. Repeating `farrow setup <template>`
over the file it generated is idempotent; a different existing file is
preserved and reported.

Setup installs missing QEMU/image/firmware/SSH/network dependencies through
Homebrew, APT, or DNF, verifies native acceleration, installs or reuses the
host-global private network, and digest-verifies the companion hosts helper.
On macOS the socket_vmnet backend comes from the version-matched Homebrew
formula by default (installed as the user when absent, then copied into
root-owned `/opt/farrow`); the digest-pinned release download —
`FARROW_VMNET_ARCHIVE`, a `FARROW_REPO` mirror, or github.com — is the
fallback. Setup prints one plan, then asks for the sudo password at most
once per run; all downloads honor the standard `HTTPS_PROXY`/`HTTP_PROXY`/
`NO_PROXY` environment variables. `--mode` is macOS-only (`host` default).
`--network-cidr` applies only to a newly generated template. The full
decision table is in [networking.md](networking.md).

## Lifecycle

```bash
farrow plan      [flags] [node...]   # classify pending changes; nothing mutates
farrow up        [flags] [node...]   # create new nodes, boot, or converge
farrow start     [flags] [node...]
farrow stop      [flags] [node...]   # alias: halt
farrow restart   [flags] [node...]   # plain stop/start; does not re-read config
farrow reload    [flags] [node...]   # stop + re-read configuration + up
farrow recreate  [flags] [node...]   # destroy then create; requires --force
farrow status    [flags] [node...]
farrow destroy   [flags] [node...]   # requires --force
```

| Flag | Accepted by | Description |
|---|---|---|
| `-f <file>` | `plan`, `up`, `reload`, `recreate` | configuration file |
| `--repo <URL-or-dir>` | `plan`, `up`, `reload` | prefer artifacts from this signed repository |
| `--force` | `destroy`, `recreate` | confirm a destructive operation |
| `--delete-persistent` | `destroy` | also delete `persistent: true` data disks |
| `--purge` | `destroy` | terminal disposal: persistent disks, the deployment keys, and the deployment state (images stay cached) |
| `--no-wait` | starting commands | return once QMP and process identity are confirmed |
| `--rollback` | `up`, `reload` | remove artifacts from nodes that failed during this prepare |

Once the deployment exists, `start`, `stop`, `restart`, `status`, and
`destroy` read the applied state, take no `-f`, and work from any directory;
supplying a configuration to `up`/`reload` is how you *change* the
deployment.

### Drift is node-granular

`plan` diffs the configuration against the applied state per node:

| plan field | Meaning | Applied by |
|---|---|---|
| `create` | hosts in the config without committed state | `farrow up` (additive; peers untouched) |
| `recreate` | nodes whose definition changed | `farrow recreate --force <node...>` |
| `missing` | stateful nodes the config dropped | `farrow destroy <node...> --force` — never automatic |

A deployment-level change (login user, subnet, defaults) is a
whole-deployment recreate. Editing non-`vm_*` inventory variables never
causes drift.

### Deletion

`destroy --force` removes node artifacts and the deployment state document,
and preserves cached images, the deployment keys, and `persistent: true`
disks (a later compatible `up` reattaches them). `destroy <node...> --force`
removes only those nodes — from the artifacts and the resolved spec — so a
later `up` does not resurrect them. `--delete-persistent` adds persistent
disks; `--purge` is the one-verb terminal disposal: persistent disks, the
keys, and the deployment state, leaving only the image cache.

## Inspection

```bash
farrow doctor
farrow logs [node] [--source serial|qemu|events] [--follow]
```

`doctor` checks QEMU, accelerator, machine/CPU/devices, an accelerated boot
smoke, firmware, OpenSSH, and network readiness. Network-readiness findings
are informational — a host that has not run `farrow setup` yet is not broken
and does not exit 3.

## Configuration

```bash
farrow init [meta|dual|trio|full] [--network-cidr <RFC1918/24>] [-o path|-] [--force]
farrow validate [-f <file>]
```

`init` writes `./farrow.yml` (refusing to overwrite without `--force`);
`-o <path>` selects another file and `-o -` prints to stdout. `validate`
parses the discovered or given configuration strictly within the `vm_*`
namespace and prints the resolved spec hash.

## Access

```bash
farrow ssh  [node]
farrow exec [node] -- <command> [args...]
farrow provision --script <path> [--sudo] [--parallel 1..4] [--timeout <duration>] [node...]
farrow ssh-config [--install|--remove] [--name <prefix>] [node...]
farrow ss [node...]
farrow hosts install|uninstall [--yes]
```

`provision` streams one bounded local Bash script (≤4 MiB, digest-audited)
to each selected guest over the verified SSH connection, serial by default.

`ssh-config --install` writes a marker-owned fragment plus one `Include` line
in `~/.ssh/config`; `farrow ss` is the shortcut, using the fixed fragment
prefix `farrow`. The fragment answers the prefixed alias (`farrow-meta`), the
bare node name (`meta`), the fixed address, and every `vm_alias` — so plain
`ssh meta` works after one `farrow ss`.

`hosts` publishes `vm_alias` names to `/etc/hosts` through the root-owned
digest-pinned helper, as one marker-owned block, on any RFC1918 subnet. It
prints the exact plan and changes nothing without `--yes`.

## Images

```bash
farrow image list|info|pull [--repo URL|DIR] [<alias>]
farrow image prune [--dry-run|--yes]
farrow image import [--sha256 <digest>] [--name <alias> --boot bios|uefi --source-user <user>] <path>
farrow image sync [--allow-downgrade] <url|path>
farrow image reset-manifest
```

See [images.md](images.md).

## Network

Host-global state, serving the one deployment:

```bash
farrow network status    [--cidr <RFC1918/24>]
farrow network install   [--cidr <RFC1918/24>] [--mode host|shared] [--yes]
farrow network uninstall [--yes]
```

Read-only preflight probes run internally before install and every lifecycle
mutation; `network status` reports the installation state plus the same
readiness findings. `install`/`uninstall` print the full privileged plan and
change nothing without `--yes`; `uninstall` refuses while any recorded node
of the deployment is live. See [networking.md](networking.md).

## Misc

```bash
farrow version
farrow completion bash|zsh|fish|powershell
```
