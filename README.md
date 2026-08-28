# Farrow

Farrow turns one Pigsty-compatible Inventory into one fixed-IP local QEMU
deployment on macOS or Linux.

Authoritative documentation: <https://farrow.pgsty.com/>

```bash
make build
export PATH="$PWD/bin:$PATH"
farrow setup --dry-run
farrow setup --yes
farrow up
```

Run the complete source gate with `make check`. Farrow is pre-1.0; a successful
build is not evidence of a tag, package publication, or supported image.

License: Apache-2.0. Dependency notices are recorded in
`THIRD_PARTY_LICENSES.md` and `third_party/licenses/`.
