# Piglet

Piglet is a native Go/QEMU runtime for Pigsty development VMs on macOS and
Linux. It provides a zero-configuration single-node quick VM and one fixed-IP
multi-node private lab without an external VM orchestrator or provider stack.

The implementation has native Tier-1 evidence for quick and exact `full`
projects on macOS arm64/HVF and Linux amd64/KVM. It is **not yet a v1.0 GA
release**. The current seven-guest formal matrix is 14/14. Production image
hosting/key custody, a durable macOS runner, current-profile native refreshes,
and several release observations remain open.
See [`docs/RELEASE_READINESS.md`](docs/RELEASE_READINESS.md) for the exact
pass/failed/blocked/not-run ledger.

## Build and inspect

```bash
make build
./bin/piglet version
./bin/piglet doctor
./bin/piglet image list
./bin/piglet completion bash
```

Quick mode works without `piglet.yaml`:

```bash
./bin/piglet up
./bin/piglet status
./bin/piglet exec -- uname -a
./bin/piglet stop
./bin/piglet start
./bin/piglet destroy --force
```

The active execution contract resolves quick as `meta`, u24, 2 CPU, 4 GiB
memory, 64 GiB root, a default sparse 64 GiB `/data`, login user `dba`
(UID/GID 88 with primary group `admin`), user
NAT SSH, and loopback forwards 15432/13000/18080/18443. This owner-finalized
baseline is recorded in
[`ADR-0001`](docs/decisions/0001-authority-and-quick-defaults.md) and
[`ADR-0008`](docs/decisions/0008-v1-owner-scope-20260824.md). All embedded
Pigsty profiles also use `dba`; their owned topology and data-disk contract is
validated inside this repository.
The Piglet-only decision is recorded in
[`ADR-0009`](docs/decisions/0009-piglet-only-dba-profiles-20260824.md).

Export a strict profile and run a private lab after explicit network install:

```bash
./bin/piglet init full >piglet.yaml
./bin/piglet validate -f piglet.yaml
./bin/piglet network preflight -f piglet.yaml
./bin/piglet network status
# Review the platform-specific plan in docs/INSTALL.md, then apply it.
./bin/piglet network install --yes   # Linux; macOS also supplies archive + UUID
./bin/piglet up -f piglet.yaml
./bin/piglet hosts install --yes
./bin/piglet ssh-config --install --name piglet
./bin/piglet stop
./bin/piglet network uninstall --yes # only after the private lease is absent
```

If the default `10.10.10.0/24` overlaps a VM, VPN, LAN, or stale vmnet
allocation, select one explicit replacement for the whole host before creating
project state:

```bash
./bin/piglet init full --network-cidr 172.31.251.0/24 >piglet.yaml
./bin/piglet network preflight -f piglet.yaml
./bin/piglet network install --cidr 172.31.251.0/24 --yes  # plus archive/UUID on macOS
./bin/piglet up -f piglet.yaml
```

Only canonical RFC1918 IPv4 `/24` ranges are accepted. Host `.1`, DHCP end
`.8`, and every node suffix move together; non-default use always prints a
warning and never silently changes an existing project.

Pigsty uses the stable `pigsty-vm` command installed on PATH. VM configuration
and Pigsty inventory are generated from the same profile, scale, and subnet:

```bash
install -d -m 0700 .piglet
PIGSTY_ROOT="$PWD" VM_SPEC=full VM_NETWORK_CIDR=172.31.251.0/24 \
  pigsty-vm inventory --output "$PWD/.piglet/pigsty.yml"
PIGSTY_ROOT="$PWD" VM_SPEC=full VM_NETWORK_CIDR=172.31.251.0/24 \
  pigsty-vm preflight
PIGSTY_ROOT="$PWD" VM_SPEC=full VM_NETWORK_CIDR=172.31.251.0/24 \
  pigsty-vm up
pigsty-vm ssh-config >.piglet/ssh_config
chmod 0600 .piglet/ssh_config
```

The inventory renderer accepts only the catalog-bound template, classifies
each address-bearing YAML semantic, preserves suffixes, adds a custom subnet
to `no_proxy`, applies resource-aware `tiny` tuning, and publishes mode-0600
output with a strict ownership/hash sidecar. It never rewrites source `conf/`.

`init` embeds all 13 Piglet-owned Pigsty profiles. `--scale 1..64` is accepted
only for profiles whose catalog policy is scalable; `deci` and `simu` remain fixed.
`--image` respects mixed-image profiles unless
`--force-uniform-image` is explicit. `--network-cidr` preserves every embedded
profile node's last octet while rebasing the one global `/24`.

Other operational commands include `plan`, `list`, `logs`, `debug bundle`,
`repair`, `image pull/import/prune/sync/reset-manifest`, `project purge-keys`,
and backup-first `project upgrade-state --dry-run|--yes`. Machine-facing output
uses `--json` where supported.

`up`, `start`, `restart`, and `recreate` accept `--no-wait` to return after
QMP/process identity instead of guest SSH/ready-marker validation. Private
`plan/up/start/stop/restart/status` accept node selectors; partial
destroy/recreate remain fail-closed and require the complete project.

Data disks marked `persistent: true` survive ordinary destroy and compatible
recreate under a strict project-owned marker store. Deletion requires both
`destroy --force --delete-persistent`. Project keys remain separate and are
removed only by a default-dry-run `project purge-keys --yes` after every node
and retained disk is gone. Private desired-state hash changes plan as explicit
destructive recreate; `recreate -f <config> --force` is the only apply path.

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=arm64 go build ./...
govulncheck ./...
make profile-contract
PIGSTY_SOURCE=/absolute/pigsty make pigsty-source-test
./tests/pigsty-wrapper-test.sh
```

Generated argv, fakes, and cross-builds are not native VM evidence. The dated
records under [`docs/evidence`](docs/evidence) distinguish unit, integration,
native real E2E, failed, blocked, and not run.

## Packaging

Development tarballs/Homebrew formula, GoReleaser archives/SPDX, RPM/DEB
payloads, final checksum assembly, and ephemeral Cosign signature/provenance
roundtrips have executable builders and strict verifiers under `packaging/`.
The release toolchain is pinned in `packaging/toolchain.env`.

```bash
./packaging/check-toolchain.sh all
goreleaser check
SOURCE_DATE_EPOCH=1787576400 PIGLET_COMMIT="$(git rev-parse HEAD)" \
  ./packaging/build-linux-packages.sh 0.1.3-next "$PWD/dist/linux-dev"
./packaging/verify-linux-packages.sh \
  0.1.3-next "$(git rev-parse HEAD)" 1787576400 "$PWD/dist/linux-dev"
./packaging/test-cosign-roundtrip.sh "$PWD/dist/linux-dev/checksums.txt"
```

Every archive/package also installs the mode-0755 `pigsty-vm` command on PATH;
the verifiers bind package build info to the expected commit and source epoch.
The checked-in push/PR workflow runs source gates; the tag workflow uses GitHub
OIDC keyless Cosign and creates a draft only. Local clean commits now exist,
but no remote/tag or approved production custody has been configured, so the
release workflow has not run. Development artifacts remain explicitly
unsigned/unattested.

## Architecture and safety

- One direct QEMU backend, native architecture only; no silent TCG fallback.
- QEMU runs as the invoking user. Quick mode needs no privilege.
- Privilege is limited to pinned private-network installation and the paired,
  root-owned hosts publisher.
- Strict YAML is user input; versioned JSON is state; qcow2 is runtime storage;
  pure-Go NoCloud CIDATA is the guest bootstrap.
- No arbitrary QEMU argv escape hatch and no global destructive `nuke`.
- Repair, destroy, prune, uninstall, and migration are ownership/path bounded
  and preserve ambiguous or persistent data.

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md),
[`docs/SECURITY.md`](docs/SECURITY.md),
[`docs/IMAGE_CONTRACT.md`](docs/IMAGE_CONTRACT.md), and
[`docs/NETWORKING.md`](docs/NETWORKING.md).

## License

Apache-2.0. Reachable third-party module versions and licenses are listed in
[`THIRD_PARTY_LICENSES.md`](THIRD_PARTY_LICENSES.md).
