# Current Quick — Darwin arm64 — 2026-08-24

Result class: **native real product E2E** for current Go 1.27 code on the
Darwin/arm64 Tier-1 host. No YAML or privileged network was used.

- Window: `2026-08-24T06:36:17Z`–`06:37:02Z`.
- Mode-0700 evidence root:
  `/Users/vonng/Library/Caches/piglet/quick-product-darwin-go127-20260824-01`.
- Piglet SHA-256:
  `d0e2e5475bc55b4bff6be5b637e0ab9d27342546d0f460798a0ba7bd2ac715ee`.
- u24 SHA-256:
  `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476`.
- Project `35a1632b-1807-4432-be24-149f19904fc4`, spec hash
  `9d9e9b91caafe5f201193640067720ab614d45515f3c6a4c5303cfe863d84206`.
- Evidence checksum-list SHA-256:
  `74d7548927d4a30c213ecb5d3152c4f7eaaf673ed78fbaa3a3c07e2608874f95`.

No-config `up` resolved and ran `meta`, `dba`, native aarch64/HVF, 2 CPU, 4
GiB RAM, 64 GiB root, one sparse 64 GiB `/data`, SSH 2222, and the exact four
business forwards 15432/13000/18080/18443. QEMU ran as user `vonng`, not root.

Product exec proved `dba`, CPU/memory/native architecture, outbound HTTPS, the
exact 64 GiB by-id data device mounted at `/data`, and wrote a canary. Public
stop/start preserved its SHA-256. Final stop left both root/data qcows clean at
exact 64 GiB virtual size. Public destroy removed node artifacts while
preserving the keypair and immutable image cache; no QEMU referenced the
project root afterward.

```text
canary-after-restart.txt  18110d3d615e73622dfe16b6e52239dd8242027fea51466a93e7bcd3dbf4ffc3
root-check.log            a0223e059b9c9df630be4cf83219dedd84830eb79d4fc73f7b07684634e14cd0
data-check.log            40fb99dcbb8c159709b72fbefbc23c8251ad8b6066cddeb88c059910e8534bf2
```

An unrelated user-NAT QEMU in another temporary project ran concurrently and
was not touched. Dedicated current-code `--no-data-disk`, four service-token
listeners, and collision-rematerialization native variants remain separate
gates.
