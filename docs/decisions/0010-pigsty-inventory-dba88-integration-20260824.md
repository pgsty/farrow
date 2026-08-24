# ADR-0010: Pigsty-owned inventory binding and final `dba` identity

- Status: accepted and native-validated
- Date: 2026-08-24

## Context

Moving only VM addresses for a custom `/24` leaves Pigsty inventory host keys,
VIPs, Redis/HAProxy/exporter references, endpoints, DNS/NTP, and proxy bypass
settings on the old network. A broad text replacement cannot distinguish those
semantics. Two profile families also have intentional topology differences:
`rpm`/`deb` are 2/5-node subsets of a seven-node build template, while `deci`
has two idle VMs not present in its eight-host inventory.

The first real Pigsty meta deployment also proved that account name alone is
not the identity contract. Pigsty expects node admin `dba` UID 88 and group
`admin` GID 88. A guest created as UID 1000 caused Ansible `usermod` to fail
because the live SSH session was using that account. Ubuntu already carried an
`admin` group with another GID, so merely setting `primary_group` was also
insufficient.

## Decision

1. Profile catalog schema 3 owns one `inventory_ref`, an `inventory_mode`, and
   any explicit unused VM nodes.
2. The inventory renderer walks a duplicate/alias/custom-tag-free YAML AST and
   changes only allowlisted address semantics. Unknown default-subnet tokens,
   VM/inventory host-set mismatch, undeclared references, and host/VIP
   collisions fail closed.
3. `rpm` and `deb` apply typed build-subset overlays: unrelated groups/hosts
   are removed, `admin_ip` and etcd move to the control VM, and `infra_seq` is
   renumbered. `deci` explicitly declares `node-8`/`node-9` idle.
4. Custom targets are canonical RFC1918 `/24`; final octets move together and
   the target is added to `no_proxy`. Control nodes below four vCPUs use
   Pigsty's `tiny` tuning from the same scale as VM resolution.
5. Inventory is published mode 0600 with a strict ownership/hash sidecar.
   `--force` can update only an unmodified Piglet-managed file.
6. Official `dba` guests create/normalize `admin` GID 88 before users-groups,
   then create `dba` UID/GID 88. Readiness verifies passwd/group IDs and home
   ownership. Custom SSH users retain normal distribution identity semantics.
7. Every profile's control node publishes the reviewed Pigsty host aliases;
   standalone and installed SSH config include those aliases.
8. `pigsty-vm` is installed on PATH by every package/archive and is the sole
   Pigsty Makefile VM entrypoint. Generated inventory and isolated SSH config
   live under ignored `.piglet/`.

## Evidence and consequences

All 13 default/custom inventories passed the typed corpus and Ansible 2.16.3
parsing with exact host counts. A native Darwin arm64/HVF U24 meta VM passed
the UID/GID/home identity contract, Pigsty bootstrap, inventory validation,
Ansible ping/become, and a complete real deploy with `failed=0` and
`unreachable=0`. The rejected UID1000 and initial GID109 runs remain evidence
that both numeric constraints are necessary.

The integration does not mutate Pigsty source templates or user global SSH
configuration. A legacy global SSH fragment is external state; validation uses
the generated project-only `ssh -F .piglet/ssh_config` path.

This decision does not close production image/signing/runner ownership or the
remaining native platform gates.
