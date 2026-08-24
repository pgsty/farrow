# ADR-0009: Piglet-only runtime and `dba` profile identity

- Status: accepted owner decision
- Date: 2026-08-24

## Decision

Piglet has one VM runtime: Piglet itself. The Pigsty wrapper no longer exposes,
reads, or dispatches a second runtime/provider path.

Every embedded Pigsty profile uses `ssh.user: dba`. The profile catalog does
not retain predecessor paths or an external source lock. The embedded YAML,
catalog policy, hashes, node counts, and resource invariants form Piglet's
self-owned contract. Dated migration evidence is provenance only.

User-authored strict YAML still has one generic `ssh.user` field; Piglet has no
special handling for any legacy account name.

## Reason

Keeping a second executable path and a legacy guest account would make the
replacement incomplete, preserve two operational and security contracts, and
force every profile, test, package, and runbook to carry a permanent rollback
surface. The product owner explicitly rejected that technical debt.

## Impact

- All 13 embedded profiles and newly generated profile YAML use `dba`.
- `packaging/pigsty/vm` invokes Piglet only and contains no alternate runtime or
  provider control.
- The profile contract verifies exact YAML digests, topology, images,
  resources, disks, and the intentional `dba` normalization without fetching
  or parsing another runtime's repository.
- Prior native evidence whose guest login was the legacy account remains a
  truthful historical execution record, but no longer proves the current
  embedded profile identity. Current profile acceptance must be refreshed.
- Release readiness no longer depends on a second-runtime comparison,
  fallback observation period, or external deprecation calendar.
