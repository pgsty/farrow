# Current Quick — Linux amd64 native KVM isolated host namespace — 2026-08-24

Result class: **native KVM public product E2E** in the same isolated Linux host
namespace. No YAML and no private-network helper were used.

## Identity and defaults

- Window: `2026-08-24T06:28:23Z`–`06:28:53Z`.
- Evidence root:
  `/data/piglet-v1-current-linux-container-20260824.bbwHvS/evidence/quick-current`,
  mode 0700.
- Go 1.27.0 linux/amd64 Piglet SHA-256:
  `3fb2601c37fe6b55a2d83c434b70cd2169a79c2b167925ebd0f0027c853d6124`.
- u24 SHA-256:
  `0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe`.
- Project `6ee879f2-1828-4731-bda2-b43ef0598709`, spec hash
  `9d9e9b91caafe5f201193640067720ab614d45515f3c6a4c5303cfe863d84206`.
- Evidence checksum-list SHA-256:
  `f0c1600dc38421a91d0d049290b9a2d00d16779ad1b9cfbb5c274c8b82711470`.

`piglet up --json` with no configuration resolved and ran `meta`, u24, `dba`,
2 CPU, 4 GiB RAM, 64 GiB root, one sparse 64 GiB `/data`, user NAT, SSH 2222,
and the four exact loopback business forwards:

```text
15432 -> 5432
13000 -> 3000
18080 -> 80
18443 -> 443
```

QEMU ran as UID 1000 `ubuntu` with KVM. Product exec proved `dba`, native
x86_64, CPU/memory, outbound HTTPS, exact 64 GiB data device/mount, and wrote a
canary. Stop/start preserved the exact canary SHA. A final stop left both 64
GiB root/data qcows clean; public destroy preserved the Ed25519 keypair and
immutable image cache while removing node artifacts.

```text
canary-after-restart.txt  e72669b221b037ced5ece922be056d342d5715e3f65071e84df78006e496506b
root-check.log            2c2c0bc26daa1d290bcd17591158a56af02e86b32263c421ff5226081bfd4891
data-check.log            40fb99dcbb8c159709b72fbefbc23c8251ad8b6066cddeb88c059910e8534bf2
```

No Piglet private-network path existed before or after this run. This refreshes
current Go 1.27 Quick defaults on Linux native KVM; dedicated current-code
forward-listener collision and `--no-data-disk` native variants remain separate
gates.
