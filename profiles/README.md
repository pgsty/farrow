# Built-in profiles

The 13 Pigsty VM topologies compiled into the binary, plus `catalog.json`,
which records each profile's override policy and its Pigsty inventory binding.
The YAML is the authoritative runtime input; the catalog describes how it may
be modified.

User-facing documentation is in [`../docs/config.md`](../docs/config.md) and
[`../docs/pigsty.md`](../docs/pigsty.md).

## Contract

Exactly 13 profiles and 85 nodes. Ordinary nodes carry one 128 GiB `/data`
disk; nodes whose name starts with `minio` carry four 32 GiB disks at
`/data1`–`/data4`. Every profile uses login `dba`, created as UID 88 with
primary group `admin` GID 88 at first boot, and publishes its Pigsty host
aliases from the control node.

`--scale` accepts 1–64 and changes only CPU and memory; `deci` and `simu`
accept scale 1 only. `all`, `deb`, `oss`, `pro` and `rpm` mix guest
distributions on purpose, so a global image override is rejected unless the
caller explicitly asks for a uniform image.

Inventory binding is explicit: 11 profiles bind directly to their template,
`rpm` and `deb` apply a typed 2-node and 5-node subset overlay to the shared
build template (control `admin_ip`, etcd placement, contiguous `infra_seq`),
and `deci` declares `node-8` and `node-9` as intentionally idle VMs.

## Adding a profile

Add exactly one YAML file and exactly one catalog descriptor. Loading is
fail-closed: unknown catalog fields, duplicate or missing entries, extra
embedded YAML, invalid strict YAML, and any filename/name mismatch are all
errors. `make profile-contract` enforces the counts and hashes.
