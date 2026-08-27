# Pigsty integration

Farrow ships `pigsty-vm`, a narrow wrapper that derives the VM topology and
Pigsty inventory from the same profile, scale, and subnet. Archives and packages
install it on `PATH`; `VM_IMAGE` affects only the VM configuration.

## Prepare and start a fresh lab

`pigsty-vm preflight` is read-only; it does not install host dependencies or
private networking. On a fresh host, first generate the exact wrapper
configuration and pass it to `farrow setup`:

```bash
install -d -m 0700 .farrow

export PIGSTY_ROOT="$PWD"
export VM_SPEC=full
export VM_NETWORK_CIDR=172.31.251.0/24

pigsty-vm init >"$PWD/.farrow/farrow.yaml"
farrow setup -f "$PWD/.farrow/farrow.yaml"

pigsty-vm inventory --output "$PWD/.farrow/pigsty.yml"
pigsty-vm up
pigsty-vm provision --script /absolute/path/to/bootstrap.sh --sudo --parallel 4

pigsty-vm ssh-config >"$PWD/.farrow/ssh_config"
chmod 0600 "$PWD/.farrow/ssh_config"
```

`setup` installs or verifies the required host capabilities and prepares the
exact generated profile. Subsequent wrapper lifecycle commands regenerate the
same strict configuration from the exported variables and act on the project in
the current directory.

With a custom `VM_NETWORK_CIDR`, use the generated SSH config or DNS for host
aliases. The optional `hosts install` publisher currently accepts only the
default `10.10.10.0/24`.

Run Pigsty against `.farrow/pigsty.yml` after the VMs are ready.

## Wrapper interface

```text
pigsty-vm up|plan|preflight|init|inventory|status|start|stop|restart|recreate
          |destroy|ssh|exec|provision|logs|repair|ssh-config|hosts|network [args...]
```

The wrapper deliberately has no `setup` subcommand. Its mappings are:

| Wrapper command | Farrow operation |
|---|---|
| `init` | `farrow init` with the selected profile overrides |
| `inventory` | `farrow pigsty inventory` |
| `preflight` | `farrow network preflight` with a generated temporary config |
| `up`, `plan`, `recreate` | matching lifecycle command with a generated temporary config |
| all other accepted commands | matching Farrow command against current project state |

Generated configs are mode 0600, strictly validated, and removed on every exit
path. Unknown wrapper commands are rejected; no command is silently upgraded to
a destructive action.

## Variables

| Variable | Default | Meaning |
|---|---|---|
| `VM_SPEC` | `meta` | built-in profile |
| `VM_SCALE` | `1` | CPU/memory scale, 1–64 |
| `VM_IMAGE` | — | global image override |
| `VM_FORCE_UNIFORM_IMAGE` | `0` | allow the image override on a mixed-distribution profile |
| `VM_NETWORK_CIDR` | profile default | host-global RFC1918 `/24` |
| `VM_ARCH` | `native` | native architecture only |
| `PIGSTY_ROOT` | working directory | Pigsty source root used by `inventory` |
| `FARROW_BIN` | `farrow` | CLI path; a value containing `/` must be absolute |

Every value is validated before use. `VM_FORCE_UNIFORM_IMAGE=1` requires
`VM_IMAGE`; unsafe names, out-of-range scale, malformed image aliases, and
invalid CIDRs fail before invoking Farrow.

Pigsty may reference the wrapper through one Makefile variable:

```makefile
PIGSTY_VM ?= pigsty-vm
```

## Inventory rendering

`pigsty-vm inventory` is equivalent to `farrow pigsty inventory`. It transforms
the catalog-bound Pigsty template for the selected profile, rebases recognised
addresses to the selected subnet, applies resource-aware `tiny` tuning, and
never rewrites the source `conf/` tree.

Unknown address semantics, residual default-subnet references, or a mismatch
between the VM and inventory node sets fail closed. Output is mode 0600 with a
digest sidecar; replacing managed output requires `--force`, while unmanaged or
hand-edited files are never adopted.

The 13 profile bindings and node counts are documented in
[Configuration](config.md#built-in-profiles).

## Guest identity

Built-in profiles create the Pigsty node administrator before Ansible connects:
`dba` is UID 88 with primary group `admin` GID 88, and readiness checks that
numeric identity. Pigsty therefore does not have to renumber its active SSH
account.
