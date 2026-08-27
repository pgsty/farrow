# Pigsty integration

The integration is the configuration format itself. Farrow reads a
Pigsty-compatible Ansible inventory, so one `pigsty.yml` is simultaneously:

- Farrow's VM specification (the `vm_*` variables),
- Pigsty's deployment inventory (everything else), and
- the human-readable record of the lab.

There is no wrapper, no rendering step, and no second file to keep in sync.
The former `pigsty-vm` wrapper and `farrow pigsty inventory` renderer are
gone.

## The flow

Inside a Pigsty checkout:

```bash
./configure -c meta     # Pigsty writes pigsty.yml
farrow setup            # discovers pigsty.yml; prepares host + network
farrow up               # boots the same file's hosts as VMs
./install.yml           # Ansible deploys against the same file
```

Pigsty's conf templates carry the `vm_*` knobs directly (they are inert
variables for anyone not using Farrow). Outside a Pigsty checkout,
`farrow init [meta|dual|trio|full]` writes a generic starter inventory as
`farrow.yml` — same format, and ready to copy into a Pigsty checkout as
`pigsty.yml` or grow into a full Pigsty config later. `pigsty.yml` remains
fully supported; when a directory contains both, `farrow.yml` wins the
discovery order (see [config.md](config.md)).

## Guest identity

Built-in defaults create the Pigsty node administrator before Ansible
connects: `dba` with UID 88 and primary group `admin` GID 88, established at
first boot so Ansible never has to renumber its own live session. Farrow
honors the inventory's own `node_admin_username`; a custom user gets an
ordinary account, and `node_admin_uid` other than 88 for `dba` is rejected
rather than half-honored.

## Name resolution

Farrow derives each VM's name the way Pigsty derives hostnames: explicit
`nodename` first, then `<pg_cluster>-<pg_seq>` (the `node_id_from_pg`
convention), then `node-<last octet>`. The two systems therefore agree on
what every machine is called without repeating anything.

Inside the guests, each node's seed carries the deployment's names and
`vm_alias` entries; on the host, `farrow hosts install --yes` publishes the
aliases (any RFC1918 subnet) so `https://i.pigsty` style access works from
the browser. Note that Pigsty's own `node_etc_hosts` management supersedes
the seed copy once `install.yml` has run — which also covers freshly added
peers that older seeds predate.

## Mixed inventories

An inventory may describe machines Farrow should not touch — real servers,
cloud instances — alongside the lab VMs. Mark them:

```yaml
    10.10.10.20: { nodename: backup-host, vm_skip: true }
```

`vm_skip: true` excludes a host from every Farrow operation while leaving it
fully visible to Ansible.
