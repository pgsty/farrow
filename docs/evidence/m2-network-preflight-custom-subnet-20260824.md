# Cross-platform network preflight and custom-subnet escape hatch — 2026-08-24

Result class: **Darwin native privileged install + real two-node E2E pass;
Linux amd64 native read-only preflight and exact privileged dry-plan pass**.
Linux host apply was not run because the available `ai` host is carrying live
Pigsty/PostgreSQL services; no production-like host network mutation is claimed.

## Reclaiming the default Darwin network

The stale Vagrant global entry claimed a running VirtualBox `meta`, while
`VBoxManage` and the project-local Vagrant status both proved that no VM
existed. `vagrant global-status --prune` removed only that cached record.
VirtualBox had no registered/running VM, host-only interface, or NAT network.

OrbStack's eleven running machines and vmnet bridge were stopped gracefully.
Calling `orbctl list` after the first stop was found to auto-start the service,
so the second stop was verified only from processes, interfaces, and routes.
No OrbStack VM data was deleted. Piglet's own daemon was booted out, after
which `com.apple.NetworkSharing` reached active count 0 and exited normally.
The physical `en0` address `192.168.0.11/24` and default gateway
`192.168.0.1` remained unchanged throughout.

Re-bootstrap of the original Piglet plan immediately recreated
`bridge100`, `10.10.10.1/24`, and the exact connected route with no 1009.
A real two-node host-mode run then passed host→`.10/.11`, VM→VM ICMP,
control lateral SSH, private no-DNS/default-route, management HTTP egress,
QMP stop, and zero runtime residue:

```text
/Users/vonng/Library/Caches/piglet/shared-retest-novbox-20260824.WW9djl/host-default-reclaimed/evidence.json
SHA-256 0c04256aea8ba35c21cb848d90ba04bbabfbff0add3049262d5d15172088005d
```

## Product behavior implemented

`piglet network preflight` is a stable typed, read-only command. It is also run
by `network install` and every private `plan/up/start/restart/recreate` before
state, disk, QEMU, stop, or destroy mutation. Findings have stable codes,
severity, error class, evidence, fix, readiness, and exit code.

The one network contract now accepts only canonical RFC1918 IPv4 `/24` ranges.
It derives host `.1`, DHCP end `.8`, and static `.9-.254`. Default remains
`10.10.10.0/24`. A non-default selection always emits a warning and must match
the installed network, resolved project, persisted state, lease, and every node.

Implemented entry points:

```text
piglet network preflight [--cidr CIDR] [-f piglet.yaml] [--json]
piglet network install --cidr CIDR ...
piglet network status [--cidr CIDR] [--json]
piglet init PROFILE --network-cidr CIDR
VM_NETWORK_CIDR=CIDR packaging/pigsty/vm ...
```

Embedded profile rebasing preserves each node's last octet and all non-network
fields. Arbitrary user YAML is never silently rebased. Network changes require
an absent active lease and explicit uninstall→install.

Preflight native controls on the Darwin host:

- installed default: ready, exact host/bridge route;
- candidate custom while default installed: state mismatch, exit 4;
- physical `192.168.0.0/24`: en0 interface + route + occupied addresses,
  resource conflict, exit 6;
- malformed, public, IPv6, non-canonical, or non-`/24`: usage error, exit 2.

The Darwin installer now preserves the separately installed
`piglet-hosts-helper` and shared `/opt/piglet` parents during network uninstall,
so subnet switching no longer needs an unsafe manual move.

## Darwin custom product E2E

The public product executor performed this complete cycle:

```text
default host 10.10.10.0/24
  → network uninstall
  → shared install 172.31.251.0/24, UUID 0d6a50d6-d7a6-44c4-baff-97de5936627c
  → standalone/status ready with non-default warning
  → two native HVF VMs at .10/.11
  → daemon restart/reconnect
  → network uninstall
  → original default host reinstall, UUID 89e9f9e5-60cb-48a0-a739-b7fa6e49cde6
```

The custom run passed host→VM, VM→VM ICMP, lateral SSH, private no DNS/default,
management HTTP egress, stream reconnect, UID-501 QEMU identity, QMP shutdown,
and zero runtime residue:

```text
/Users/vonng/Library/Caches/piglet/shared-retest-novbox-20260824.WW9djl/product-custom-shared/evidence.json
SHA-256 2467b1bdd261de5f3ddfcdd2633816dbe74acca3b6975db9c97a4f6c3fd33bb9
```

All four stopped custom qcow2 files passed `qemu-img check`. The root-installed
hosts helper remained
`4d218aef0f83baf20a9bd78d8e2bc76b1bae084cb0371ab22be6d7f6be6f8942`
through both uninstall/install cycles.

## Linux amd64 evidence

On authorized native host `ai` (Ubuntu 24.04, amd64, KVM), standalone
preflight reported both default `10.10.10.0/24` and candidate
`172.31.252.0/24` clean while correctly observing unrelated physical,
Wi-Fi, Docker, and libvirt ranges.

The exact custom install dry-plan passed and recorded:

- Debian family and package-owned helper prestate root:root 0755;
- reversible root:kvm 4750 `dpkg-statoverride`;
- `piglet0` address `172.31.252.1/24`;
- DHCP end `172.31.252.8` in the manifest;
- NetworkManager unmanaged rule before bridge activation;
- networkd's four-unit prestate and reversible persistence phases;
- non-default warning in JSON.

The remote host remained unchanged: no `/run/piglet`, `piglet0`, managed files,
helper override, QEMU, or lease was created. A privileged host apply requires a
dedicated disposable Linux runner rather than the live `ai` service host.

## Final local binaries

```text
piglet                   a8c7795cac5f90465dde0b2c6f947c36a0c90d6659b462f3884341c5019f22d4
piglet-hosts-helper      4d218aef0f83baf20a9bd78d8e2bc76b1bae084cb0371ab22be6d7f6be6f8942
piglet-private-m0        ced62655f79eaae042383c7e41b7368ed3f928f901b785e96356f0ce317a3aa4
piglet-net-stage         59476df56b4b3a59a5b0873601343faee5317196db446a7114e783a4a6a1fc42
piglet-linux-net-stage   562c144a9dc71c539b06eca1ebdbf1adeffcdfd297537f955680d9a22feedb24
```

`go version -m bin/piglet` reports Go 1.27.0 and embeds the exact companion
hosts-helper digest. Full unit/race/vet/Staticcheck/govulncheck, four-target
compile, wrapper, license, ShellCheck, Actionlint, and GoReleaser checks passed.
