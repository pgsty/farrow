# Embedded Pigsty profiles

This directory contains the 13 Pigsty VM specifications supported by Piglet.
Both `catalog.json` and the YAML files are compiled into the binary. The
schema-3 catalog records Piglet's current override policy and one typed Pigsty
inventory binding per profile. It contains no predecessor-runtime paths or
source checkout dependency.

The YAML is the reviewable and authoritative runtime input. Ordinary nodes
have a 128 GiB `/data` disk, while every node whose name begins with `minio`
has four 32 GiB disks mounted at `/data1` through `/data4`. The owned contract
contains exactly 13 profiles and 85 nodes.

All embedded profiles intentionally normalize the guest login to `dba` and
publish the reviewed Pigsty host aliases from their control node. The guest
contract creates `dba` as UID 88 with primary group `admin` GID 88, matching
Pigsty's node-admin identity before Ansible connects. The
dated migration evidence remains historical provenance only; it is not part of
the catalog schema, release workflow, runtime, or account settings.

Inventory mode is explicit. Eleven profiles bind directly to their template;
`rpm` and `deb` apply a typed 2/5-node subset overlay to the shared build
template, including control-node `admin_ip`, etcd placement, and contiguous
`infra_seq`. `deci` declares `node-8` and `node-9` as intentional idle VMs.
Unknown address semantics or VM/inventory host-set drift fail closed.

`--scale` accepts 1 through 64 and changes only CPU and memory. The `deci` and
`simu` profiles accept only scale 1. The `all`, `deb`, `oss`, `pro`, and `rpm`
profiles intentionally mix guest distributions, so a global image override is
rejected unless the caller explicitly requests a uniform image.

Do not add a YAML file without adding exactly one catalog descriptor. Catalog
loading is fail-closed: unknown catalog fields, duplicate or missing entries,
extra embedded YAML, invalid strict YAML, and a filename/name mismatch are all
errors.
