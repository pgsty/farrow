# Pigsty integration

Pigsty drives Farrow through one stable command on `PATH`: `pigsty-vm`. Every
archive and package installs it. The point of the wrapper is that the VM
topology and the Ansible inventory are derived from the *same* profile, scale
and subnet, so the two can never drift apart.

## Bringing up a lab

```bash
install -d -m 0700 .farrow

export PIGSTY_ROOT="$PWD"
export VM_SPEC=full
export VM_NETWORK_CIDR=172.31.251.0/24

pigsty-vm inventory --output "$PWD/.farrow/pigsty.yml"
pigsty-vm preflight
pigsty-vm up
pigsty-vm provision --script /absolute/path/to/bootstrap.sh --sudo --parallel 4

pigsty-vm ssh-config >.farrow/ssh_config
chmod 0600 .farrow/ssh_config
```

Then run Pigsty as usual against the rendered inventory.

## Wrapper interface

```
pigsty-vm up|plan|preflight|init|inventory|status|start|stop|restart|recreate
          |destroy|ssh|exec|provision|logs|repair|ssh-config|hosts|network [args...]
```

Everything except `inventory` forwards to the matching `farrow` command with
the profile, scale, image and subnet already resolved. Unknown subcommands are
rejected rather than passed through.

For `up`, `plan`, `preflight` and `recreate`, the wrapper renders a mode-0600
temporary strict YAML through `farrow init`, validates it, passes it with `-f`,
and removes it on every exit path. It never adds an arbitrary QEMU argument
path and never silently upgrades a command to a destructive one.

Pigsty references the wrapper through a single Makefile variable:

```makefile
PIGSTY_VM ?= pigsty-vm
```

| Variable | Default | Meaning |
|---|---|---|
| `VM_SPEC` | `meta` | profile name |
| `VM_SCALE` | `1` | 1–64; multiplies CPU and memory only |
| `VM_IMAGE` | — | global image override |
| `VM_FORCE_UNIFORM_IMAGE` | `0` | allow `VM_IMAGE` on a mixed-distribution profile; requires `VM_IMAGE` |
| `VM_NETWORK_CIDR` | `10.10.10.0/24` | host-global `/24` |
| `VM_ARCH` | `native` | native architecture only; a foreign value is an error |
| `PIGSTY_ROOT` | working directory | Pigsty source root, used by `inventory` |
| `FARROW_BIN` | `farrow` | explicit CLI path; must be absolute if it contains a slash |

Every value is validated before use — an unsafe profile name, out-of-range
scale, malformed image alias or non-`/24` CIDR fails with exit 2.

## Inventory rendering

`pigsty-vm inventory` (equivalently `farrow pigsty inventory`) is a narrow
YAML-AST transformer over a catalog-bound Pigsty template. It is not a generic
Pigsty installer.

It classifies every address-bearing value in the template, rebases inventory
host keys, admin addresses, L2 VIPs, service references, DNS/hosts/NTP entries
and endpoints onto the selected subnet while preserving each final octet, adds
a custom subnet to `proxy_env.no_proxy`, and applies resource-aware `tiny`
tuning. Source `conf/` is read-only and never rewritten.

Anything it cannot classify is a hard error. Residual references to the default
subnet, unknown address semantics, or a mismatch between the VM topology and
the inventory host set all fail closed rather than emitting a half-rebased file.

Output is secret-bearing configuration, so it is published mode 0600 next to a
strict JSON sidecar recording source and output digests, profile, scale, subnet
and inventory mode. Replacing an existing output requires `--force` *and* a
current file whose digest still matches its sidecar — unmanaged, hand-edited,
symlinked, cross-user or multiply-linked files are never adopted.

## Profile bindings

Each of the 13 profiles binds to exactly one inventory template with an
explicit node policy:

- 11 profiles bind directly to their template;
- `rpm` and `deb` apply a typed 2-node and 5-node subset overlay to the shared
  build template, including the control node's `admin_ip`, etcd placement and
  contiguous `infra_seq`;
- `deci` declares `node-8` and `node-9` as intentionally idle VMs.

See [config.md](config.md) for the profile list and node counts.

## Guest identity

Farrow resolves the Pigsty node-admin identity before Ansible ever connects:
cloud-init creates `dba` as UID 88 with primary group `admin` GID 88 and gates
readiness on the numeric identity. Account creation therefore happens outside
the live provisioning session, and Pigsty's node-admin task is idempotent
instead of trying to renumber the account it is currently logged in through.
