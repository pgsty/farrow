# Current `full` profile — Darwin arm64 — 2026-08-24

Result class: **native real product E2E** for the current all-`dba`
`profiles/full.yaml` on the Darwin/arm64 Tier-1 host.

## Identity

- Window: `2026-08-24T06:32:43Z`–`06:33:57Z`.
- Mode-0700 retained root:
  `/Users/vonng/Library/Caches/piglet/full-product-darwin-dba-go127-20260824-01`.
- Go 1.27.0 Piglet SHA-256:
  `d0e2e5475bc55b4bff6be5b637e0ab9d27342546d0f460798a0ba7bd2ac715ee`.
- Profile SHA-256:
  `454769bb0883a775e9360aeac822653efca72b39754a500a7d6483f92063a601`.
- u24 arm64 SHA-256:
  `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476`.
- Project `7bbe2692-e531-4ed8-8fd3-b51a3c30dfd2`, spec hash
  `d7bbf904a9218856acc6f984d2982236e8be243e2726f017848a18295e59c0ab`.
- Evidence checksum-list SHA-256:
  `513e355652f846e3ca155cba9d949772361d8a295495453f3130156be8058bb6`.

## Result

Complete typed preflight returned the pinned host-mode installation as
healthy/protected on `bridge100`, exact `10.10.10.1/24` host and `/24` route,
no finding, and inactive lease. Public plan was non-destructive create. Public
up reached:

```text
meta    10.10.10.10  control  dba  2 CPU / 4 GiB  NAT SSH 2222
node-1  10.10.10.11           dba  1 CPU / 2 GiB  NAT SSH 2223
node-2  10.10.10.12           dba  1 CPU / 2 GiB  NAT SSH 2224
node-3  10.10.10.13           dba  1 CPU / 2 GiB  NAT SSH 2225
```

All four native aarch64/HVF guests proved exact private IP, management-only
default route, outbound HTTPS, host ICMP/direct SSH, and `dba` CPU/memory
identity. `meta` owned the mode-0600 `dba:dba` lateral key and reached every
worker by name; workers had no private key.

Each node had a 64 GiB root and one exact 128 GiB XFS `/data` disk. Four
by-id/size/mount/UUID/fstab contracts passed, all canary SHA values matched
after stop/start, and a final stop left all eight qcows clean with exact virtual
sizes.

```text
disk-contract.tsv           1dd9751814d98098fab3789d93339b6873f5b93d1bec68be28dcec3c6663f314
canaries-after-restart.tsv  5866e977eccff28fa6879715d5e599bb23d4d79d72697d82bc7b8f49336313dd
qemu-img-check.log          50a06275cb5c67d1ebd23146e927c1816f64addccba8c1ee29f7bbcf986b4033
```

Public destroy returned all four nodes absent, preserved the keypair and
immutable cache, and removed every project QEMU. Final global network status
remained healthy/protected with an inactive lease. An unrelated user-NAT Quick
QEMU in another temporary project was not touched.

This refreshes current Darwin `full` identity/topology/storage semantics. The
earlier Darwin full evidence remains the authority for crash repair, host
integrations, and the 30-cycle soak; this run does not repeat those gates.
