# Security model

Farrow is a single-user development VM manager. It is not a sandbox for
hostile guests, and it does not isolate you from other users on the same host.
Read the limitations at the end before using it anywhere that matters.

## Privilege boundary

- QEMU always runs as the invoking user. Never as root.
- On Linux, `setup` may use sudo once to install missing system packages
  through APT or DNF.
- On macOS, exactly one privileged component exists: a pinned `socket_vmnet`
  daemon, running as root from a root-only path.
- On Linux, exactly two: root-owned bridge persistence, and the distribution's
  own `qemu-bridge-helper`.
- The optional `/etc/hosts` publisher is a separate root-owned helper.

No privileged component ever executes a user-writable Farrow binary, accepts a
shell string, or takes arbitrary argv. Privileged steps invoke
`sudo -n -- <absolute-system-binary>` with fixed arguments.

The release CLI is digest-paired to its companion hosts helper: the helper's
SHA-256 is compiled into the CLI, and the CLI verifies the fixed path, every
parent directory, owner, group, mode, link count and file type before invoking
it. A CLI built without that digest injection skips the pairing check — build
with `make build` or an official package, never a bare `go build`.

## Changing host state

`network install`, `network uninstall` and `hosts install` all follow the same
rule: print the exact plan — paths, ownership, modes, subnet, rollback — and
change nothing until `--yes`.

The readiness preflight probes run again immediately before mutation, so a
host that changed between review and apply fails instead of proceeding.

Uninstall re-checks ownership, content hashes and override state, removes
state last, and uses `rmdir` for owned directories so a non-empty one is
never blown away. It audits the runtime identity of every recorded node
first; any live node blocks uninstall with no mutation at all.

Linux staged content is re-hashed before installation, and an unprivileged
QEMU/QMP attach must succeed before bridge persistence is enabled (via
systemd-networkd units or an owned NetworkManager connection, whichever
manager owns the host). macOS
verifies the socket_vmnet archive and both extracted binary digests before any
privileged step, and uninstall boots out only the pinned launchd label.

When macOS setup sources socket_vmnet from the version-matched Homebrew
formula instead of the release archive, the per-binary digests are computed
at install time and recorded in the root-owned network state and the public
identity marker; staging re-hashes the copies against those recorded values,
and every later verification pins against them. This is trust-on-first-use
for the initial bytes — the same trust already extended to Homebrew for QEMU
itself, which runs the guests — while preserving the invariant that root only
ever executes binaries from root-owned paths, never the user-writable brew
keg. `brew` itself is always run as the invoking user.

`farrow setup` asks for the sudo password at most once per run: the first
privileged step explains itself and prompts, then a background keeper
refreshes the cached credential until setup finishes, so a slow package
installation or download can never trigger a second prompt.

## Deletion

Directories holding runtime state, QMP sockets, keys and seeds are `0700`;
private keys and seeds are `0600`.

Before `destroy`, `prune`, or `network uninstall`, Farrow:

1. resolves and canonicalizes the exact target;
2. rejects symlinks, unexpected file types, empty or unresolved paths, and
   broad roots such as your home directory or the working directory;
3. verifies containment beneath the owned data root and matching ownership;
4. preserves persistent disks and the deployment keys unless the specific
   destructive flag is present.

There is no global `nuke`. If ownership or process identity cannot be proven,
the resource is preserved and the command reports exactly what manual evidence
is missing.

Key deletion happens only inside `destroy --force --purge`. It refuses while
any node artifact or retained disk exists, and removes only the fixed
allowlist `id_ed25519`, `id_ed25519.pub`, `known_hosts`, `known_hosts.old` —
never a widened glob.

## Keys and the guest

The deployment has one Ed25519 key pair and one `known_hosts`, under
`~/.farrow/keys/`. Host key checking is never globally disabled. OpenSSH
option values and generated config paths are internally double-quoted, so a
`FARROW_HOME` containing spaces cannot split the `known_hosts` path into a
home-directory file.

Only a multi-node **control** guest receives the deployment private key, for
lateral SSH to its peers. Single-node labs never receive a lateral key.

### Explicit guest provisioning

`farrow provision` is an SSH client operation, not a provider or plugin
boundary. It rejects empty, oversized, non-regular and final-component symlink
scripts, reads one bounded immutable byte snapshot, and sends that snapshot on
stdin to the fixed guest argv `/bin/bash -se`. `--sudo` changes that fixed argv
to `sudo -n -- /bin/bash -se`; it never elevates the Farrow or QEMU process on
the host.

Provisioning requires QMP- and process-verified running nodes and reuses the
deployment key and strict deployment `known_hosts`. It holds the exclusive
deployment lock for the complete bounded operation, so lifecycle commands
cannot stop or destroy a VM underneath a running script. Events contain the
script SHA-256, size, node names, exit codes and durations, but never its
host path, body, or captured output. Per-node stdout and stderr are returned
to the caller with a 1 MiB cap each and are not added to event logs.

Because cloud-init can run `write_files` before `users-groups`, the control key
and config are staged `root:root` 0600 and installed by a fail-closed finalizer
that first resolves the account's numeric UID/GID and canonical home path.
Cloud-init parses the injected key, requires Ed25519, and compares its derived
public key against the deployment public key. Staging paths are removed by
both the installer and an EXIT trap.

Seed media and retained end-to-end test artifact directories do contain
generated key material; treat them accordingly.

## Ready marker

The guest writes its ready marker only after verifying disk identity and
mounts, the account's numeric identity, and — for private nodes — that
`private0` is up with the right address, no default route and no DNS.

Mount paths must be canonical absolute paths and cannot traverse into reserved
system trees. An already-mounted path must present the expected filesystem
UUID.

## Supply chain

Remote images require a pinned digest and exact byte count from the signed
catalog. Local filenames remain readable under one image-family directory and
are rejected if they carry backing files, external data files, encryption or
unknown incompatible features. Catalog updates are detached-signature verified
against trusted keys; repository HTTP is accepted only because both catalog
and selected bytes are independently authenticated, while upstream fallback
must be HTTPS.
Ordinary builds ship no external catalog roots until active and standby
production custody is assigned; external activation therefore fails closed,
while the embedded bootstrap catalog remains available.

Archive and package verifiers recompute the companion helper's SHA-256 and
require those exact bytes inside the paired CLI, then validate payload paths,
file types, modes and architecture before publication. SBOMs, formula, release
metadata and provenance predicate are all covered by one final checksum
manifest.

Production signing keys are not in this repository, and image catalog keys are
a separate trust domain from release signing keys.

## Known limitations

- Loopback port forwards block the LAN but not other local users on your host.
- Farrow detects a foreign occupant of its subnet and refuses; it cannot stop
  one from appearing later, and it does not arbitrate with other hypervisors.
- A guest with root access can do anything a normal process on your host
  network can do. Farrow is not a containment boundary.
- A configured 9p share intentionally gives the guest access to that host
  directory. Read-only is the default, but this is still for trusted laboratory
  guests and source trees only; never export secrets, Farrow state, VM images,
  or production data.
