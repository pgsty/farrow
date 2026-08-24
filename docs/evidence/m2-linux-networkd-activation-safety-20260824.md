# Linux inactive-networkd activation safety — 2026-08-24

Result class: **unit/race PASS + native Linux amd64 read-only refusal PASS**.
No privileged host-network mutation was performed.

## Implemented boundary

When `systemd-networkd` is already active, the established Piglet plan is
unchanged. When it is inactive, discovery now inventories current links,
alternative names and types plus the effective `.network`/`.netdev` files in
systemd precedence order. Before staging or sudo mutation, plan requires proof
that every effective non-Piglet network rule is disjoint from every existing
link and the planned `piglet0`.

The gate fails closed on:

- a positive Name/Type match against an existing link;
- unsupported or ambiguous match syntax that prevents a disjoint proof;
- any non-Piglet `.netdev` that could create a virtual interface;
- drop-ins, unsafe/mutable/unreadable files or directories;
- changed Piglet-owned networkd content.

Masked `/dev/null` units and exact priority shadowing are handled explicitly.
Fixtures cover an `eth0` collision, alternative names, drop-ins, non-Piglet
netdevs, and stock Ubuntu vendor rules that are provably disjoint.

## Native `ai` result

The current Go 1.27 Linux/amd64 binary had SHA-256
`05e79aee68661d5f315550c9eff027e735a7883eccef9a39368eae9c6abc6bf9`.
On `vonng-aimax` (Ubuntu 24.04 amd64), standalone network preflight found the
default `/24` eligible and returned exit 0. The host's networkd service was
disabled/inactive.

The install dry-plan then stopped before staging with exit 7:

```text
refuse to start inactive systemd-networkd: /usr/lib/systemd/network/80-wifi-adhoc.network could affect link wlp193s0
```

The refusal text SHA-256 was
`f7b493a20ba04c2e8239003f0b9b16f4c5afd5571d9d8cb8e9d3c4325d3a51b9`.
That vendor rule has `Type=wlan` plus `WLANInterfaceType=ad-hoc`. Because the
supported floor cannot safely use the latter predicate as proof, the actual
wireless link remains a conservative conflict.

Postflight proved networkd still inactive/disabled, `piglet0` absent, and all
four Piglet unit/state/lease paths absent. This is the intended safety result:
Piglet did not start networkd, claim Wi-Fi, alter a system address/route, or
write a privileged plan artifact.

## Tests

```text
go test -count=1 ./internal/network/linux
go test -race -count=1 ./internal/network/linux
go vet ./internal/network/linux
```

All passed. A fresh native Ubuntu install whose effective rules are provably
disjoint still requires a new privileged E2E after this source change; the
earlier install/two-VM/uninstall evidence predates the activation gate.
