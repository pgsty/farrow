# Pigsty VM runtime integration

Piglet ships independently from Pigsty. Pigsty has one VM runtime entrypoint:

```makefile
PIGSTY_VM ?= pigsty-vm
```

The wrapper translates a small, typed environment at the integration edge:

| Integration input | Piglet translation |
| --- | --- |
| `VM_SPEC` | embedded profile name, default `meta` |
| `VM_IMAGE` | `piglet init --image` |
| `VM_SCALE` | `piglet init --scale`, 1–64 |
| `VM_NETWORK_CIDR` | VM profile plus Pigsty inventory/VIP rebase, explicit RFC1918 `/24` |
| `VM_ARCH` | must be native for Piglet |

For `up`, `plan`, `preflight`, and `recreate`, the wrapper renders a mode-0600 temporary strict YAML with
`piglet init`, validates it, passes it with `-f`, and removes it on every exit.
It does not add an arbitrary QEMU argument path or silently force destructive
commands. Mixed-distribution profiles require both `VM_IMAGE` and
`VM_FORCE_UNIFORM_IMAGE=1`.

Examples:

```bash
install -d -m 0700 .piglet
PIGSTY_ROOT="$PWD" VM_SPEC=full pigsty-vm inventory --output "$PWD/.piglet/pigsty.yml"
PIGSTY_ROOT="$PWD" VM_SPEC=full pigsty-vm preflight --json
PIGSTY_ROOT="$PWD" VM_SPEC=full pigsty-vm up
pigsty-vm ssh-config >.piglet/ssh_config
chmod 0600 .piglet/ssh_config
ssh -F .piglet/ssh_config meta
pigsty-vm destroy --force
```

The same entrypoint exposes typed `inventory`, `network`, `logs`, `repair`, `recreate`,
`ssh-config`, and `hosts` commands; it never translates arbitrary QEMU flags.
Release archives, Homebrew, RPM, and DEB all install `pigsty-vm` on PATH.

`pigsty-vm inventory` accepts only the selected profile's schema-3
`inventory_ref`. It classifies host keys, admin/VIP fields, HAProxy/Redis/
exporter/portal references, DNS/hosts/NTP lists, rejects unknown semantics,
preserves final octets, and requires the resulting host set to match the VM
contract. `rpm`/`deb` use explicit build-subset overlays and `deci` declares
two idle VMs. Source templates are read-only. Generated inventory and its
ownership/hash sidecar are mode 0600; `--force` only replaces an unchanged,
previously managed output.

For custom subnets the renderer also adds the selected `/24` to `no_proxy`.
For control nodes below four vCPUs it applies Pigsty's `tiny` node/PostgreSQL
tuning, using the same `VM_SCALE` as VM resolution.

The former runtime/provider selector controls are removed, not deprecated. The
wrapper neither reads nor forwards them. Every generated embedded profile uses
login user `dba`; cloud-init creates the final Pigsty identity as UID/GID 88
before readiness, so node provisioning never changes an active SSH user's UID.

Before changing Pigsty's default, require both Tier-1 quick/full evidence,
the owned profile contract, build-matrix smoke, installation/upgrade/troubleshooting docs,
self-hosted signed manifests, and a release-readiness review with no P0
blocker. Operational rollback is to a previously published, verified Piglet
artifact and state backup; it is not a second provider path.
