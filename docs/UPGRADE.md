# Upgrade and state compatibility

Upgrade only while project VMs are stopped and the host-global private lease is
absent. Keep the old binary and package until the new binary passes `doctor`,
`status`, and a stop/start smoke.

## State N-1 migration

Schema-1 readers refuse newer state and never downgrade it. The explicit
schema-0 compatibility path is reviewable and backup-first:

```bash
piglet project upgrade-state --dry-run --json
piglet project upgrade-state --yes --json
```

The dry run is non-mutating. Apply acquires the project lock, validates the
entire project/node/transaction set before writing, creates mode-0600 backups
at the project root, rechecks source bytes, and atomically publishes each
strict schema-1 file with the new `piglet_version`. It is idempotent. A backup
whose bytes do not match the source blocks migration.

The CLI enforces the host-global lease absence and returns exit 6 if a private
project is active. Each present node must be `absent`, `prepared`, or `stopped`
with an empty process identity; running/transitional/stale-process state fails
before any backup is created.

If a newer schema is encountered, stop immediately and use a binary that
understands it. Do not hand-edit the schema number. A failed migration keeps
the original backups; inspect them and retry with the same or corrected binary
rather than deleting state/disks.

## Binary/package upgrade

1. Stop every project and record `piglet list --json` plus `status --json`.
2. Verify release checksum, SBOM and signature bundle.
3. Replace the CLI/package; package managers also replace the root-owned hosts
   helper with its paired digest.
4. Run state dry-run/apply in each retained workspace.
5. Run `doctor`, `plan`, `start`, `exec -- true`, and `stop`.
6. Keep migration backups through at least one successful lifecycle and the
   normal backup-retention period.

## Network upgrades

Network state is host-global and separately privileged. Never overwrite it by
copying files. With no active lease, run public `network uninstall` to review
the rollback, apply it, then review/apply the new `network install`. macOS must
reuse the persistent interface UUID. Linux must prove helper/networkd/NM
prestate restoration before reinstall.

## Rollback

Binary rollback is allowed only when the retained state schema is understood by
the old binary. Schema-1 is the v1 floor. If the new binary migrated state,
restore only from the exact mode-0600 backup while all VMs are stopped and
after comparing project IDs/spec hashes; never roll back runtime disks with a
state file from a different generation.
