# Security model

Piglet is a single-user development VM manager, not a sandbox for malicious
guests or mutually untrusted host users.

## Privilege boundary

- QEMU always runs as the invoking user.
- Quick/user networking never needs sudo or a helper.
- On macOS only a root-owned pinned `socket_vmnet` daemon is privileged.
- On Linux only root-owned bridge persistence and the distribution bridge
  helper cross the privilege boundary.
- A privileged service never executes a user-writable Piglet binary or accepts
  shell strings/arbitrary argv.

Network install/uninstall must display the exact paths, ownership, mode,
subnet, and rollback before changing the host. The initial audits made no
privileged changes. Later M0 execution applied the displayed plans with fixed
root-owned system tools: the macOS daemon remains installed, while the Linux
clean-host experiment restored every original helper/unit/path state and left
no host-network residue. At no point did QEMU run as root.

Linux product install/uninstall now preserves that boundary: dry-run is the
default, `--yes` invokes only `sudo -n -- <absolute-system-binary>` argv, staged
fixed content is rehashed before install, and a non-root QEMU/QMP helper smoke
must pass before networkd is enabled. Uninstall holds the shared lease flock,
rechecks lease/member/hash/override state, removes state last, and uses `rmdir`
for owned directories. It refuses an active real product lease without any
mutation.

Darwin product install applies the same dry-run/`--yes` model. The unprivileged
side verifies the embedded socket_vmnet archive and both extracted binary
digests, while fixed sudo `install`/`launchctl` argv own the privileged side.
Uninstall holds the lease flock, refuses QEMU socket users or unexpected
entries, boots out only the pinned label, removes exact verified files/logs,
and uses `rmdir` for dedicated empty paths. Fresh reinstall recreates a
root-owned 0666 lease lock so ordinary lifecycle code never needs to chmod a
root inode.

## Files, processes, and deletion

Runtime, QMP, key, and seed directories are `0700`; private keys and seeds are
`0600`. Before signalling, Piglet validates QMP name/UUID plus process identity.

Before destroy, prune, repair deletion, or uninstall, Piglet must:

1. resolve and canonicalize the exact target;
2. reject symlinks, unexpected file types, empty/unresolved paths, and broad
   roots such as home/workspace/XDG root;
3. verify containment beneath the owned data root and match ownership metadata;
4. preserve persistent disks and project keys unless the dedicated destructive
   flags/commands and confirmations are present.

Debug bundles use a fixed input allowlist, bounded reads, content redaction,
mode-0600 atomic publication, and no-overwrite semantics. They never collect
seed contents, disks, project keys/known-hosts, process environment, or
arbitrary project files. Redaction is defense in depth; users must still review
the printed file list and manifest before sharing a bundle.

Project key purge is a separate dry-run-first command. It refuses while any
node directory exists, preflights the complete mode-0700 key directory, and
accepts only `id_ed25519`, `id_ed25519.pub`, `known_hosts`, and
`known_hosts.old`; `--force` never broadens that allowlist. Recreate likewise
requires `--force` and composes the existing guarded destroy and transactional
up paths rather than introducing a global cleanup primitive.

The private lease uses an installer-created root-owned sticky runtime root, a
shared flock inode, and an active-UID-owned atomic lease. It contains no secret,
but its paths, owner, modes, strict schema, reservation uniqueness, heartbeat,
QMP identity, process identity, and pidfile are all verified before release or
reclaim. Another UID cannot perform silent stale takeover.

## Guest and secret boundary

Each project has an Ed25519 key and isolated `known_hosts`. Only a multi-node
control guest receives the private key. Quick never receives a lateral private
key. Host key checking is not globally disabled. OpenSSH option/config paths
are internally double-quoted (not merely passed as one process argv), so the
default macOS `Library/Application Support` root cannot split the per-project
known-hosts path into a home-directory wildcard file.

Cloud-init parses the injected control private key, requires Ed25519, and
compares its derived public key with the project public key. Because
`write_files` can run before `users-groups`, the control key/config are staged
root:root 0600 and installed only by a fail-closed finalizer after resolving
the account's numeric UID/GID and canonical `/home` path. Exact staging paths
are removed by both the installer and an EXIT trap. Seed media and retained
E2E artifact directories still contain sensitive generated key material and
are excluded from debug bundles.

Official profiles use the final Pigsty node-admin identity from first boot:
`dba` UID 88 with primary `admin` GID 88. Early group setup fails when GID 88
is occupied by an unrelated group, and the final identity contract fails
readiness on any passwd/group/home mismatch. Pigsty therefore never needs to
change the UID of its live SSH/Ansible session.

Pigsty inventory output is secret-bearing configuration. It is published mode
0600 beside a strict JSON sidecar containing source/output digests, profile,
scale, subnet, and inventory mode. Replacement requires both `--force` and a
current output whose digest still matches its marker; unmanaged, edited,
symlinked, cross-user, or multiply linked files are never adopted.

The ready marker is written only after disk identity/mount checks and, for
private guests, exact `private0` UP/address/no-default/no-DNS checks. Mount
paths must be canonical absolute paths and cannot traverse into reserved
system trees; an already mounted path must have the expected filesystem UUID.

Debug bundles exclude seed contents, private keys, passwords, authorization
headers, and tokens. Redaction tests use generated canaries.

Loopback forwards prevent remote-LAN access but do not isolate other local
users on the same host. This limitation must remain visible in user-facing
documentation.

## Supply chain

Remote images require a pinned digest. Local images are imported into a
digest-addressed managed cache and rejected when they contain backing files,
data files, encryption, or unknown incompatible features. Manifest updates are
explicit and detached-signature verified; production signing keys never enter
this repository.

Release CLIs are digest-paired to the exact companion hosts helper. Archive and
package verifiers recompute the helper SHA and require those bytes inside the
CLI, then validate exact payload/type/mode/architecture before publication.
Payload/archive SBOMs, formula, release metadata, and provenance predicate are
covered by one final checksum manifest. The tag workflow uses an exact GitHub
OIDC identity to sign and attest that manifest, creates only a draft, and
refuses overwrite. The workflow is unrun and does not replace the missing
production publisher/custody decision.

Exact license texts for all six linked external modules ship under
`third_party/licenses/`; `packaging/verify-licenses.sh` fails if either the
reachable module set or those bytes drift.
