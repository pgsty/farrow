# ADR-0001: Authority and quick defaults

- Status: accepted and finalized by product owner
- Date: 2026-08-23

## Facts

`docs/003-prd.md` v2.1 declares itself the product/architecture baseline, but
its former quick examples and acceptance text used a legacy login plus a 128
GiB data disk.
The repository's later `docs/004-implementation-prompt.md` and the active goal
prompt explicitly label `dba` and a 64 GB quick data disk as terminal semantics.
Both sources continue to require 128 GiB `/data` for ordinary migrated Pigsty
profiles and four 32 GiB disks for `minio*`.

## Decision

Implement quick with `ssh.user=dba` and a 64 GiB `/data` disk. Keep migrated
profile parity independent: ordinary profile nodes receive 128 GiB `/data` and
`minio*` nodes receive four 32 GiB disks.

On 2026-08-24 the product owner explicitly finalized `64 GiB + dba` as the
current baseline. The conflicting quick examples/acceptance clauses in 003/004
are updated accordingly; this is no longer an open release decision.

## Impact

This chooses the newer direct execution instruction without silently changing
profile data semantics. Tests must assert quick and profile storage defaults
separately. ADR-0009 later standardized all embedded profile logins on `dba`;
known public development credentials are never propagated into official
images.
