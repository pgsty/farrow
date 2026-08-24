# M0 host and repository audit — 2026-08-23

Result class: read-only audit. No production host, privileged network path, or
destructive action was touched.

## Workspace

- CWD: `/Users/vonng/pgsty/piglet`
- Initial contents: `.DS_Store` and `docs/000-raw-demand.md` through
  `docs/004-implementation-prompt.md`
- Initial Git state: not a Git repository
- Action after audit: initialized an empty `main` repository; preserved all
  existing docs byte-for-byte

## Host

- OS: macOS 26.5.2 (build 25F84), Darwin 25.5.0
- Arch: arm64 (`RELEASE_ARM64_T6000`)
- CPU/RAM: 10 logical CPUs, 64 GiB
- Go: `go1.26.5 darwin/arm64` at `/opt/homebrew/bin/go`
- staticcheck: 2025.1.1 (0.6.1)
- Filesystem: about 1.1 TiB available on `/System/Volumes/Data`
- Open-file limit: 1,048,575

## QEMU, accelerator, and firmware

- `qemu-system-aarch64`: absent
- `qemu-system-x86_64`: absent
- `qemu-img`: absent
- QEMU Homebrew package: absent
- Firmware candidates: none found in the audited Homebrew/system paths
- HVF real smoke: `blocked`; no QEMU binary, so no claim is made
- KVM: not applicable on this host; Linux amd64 runner is missing

The normal `brew install qemu` attempt fetched no usable package metadata,
reported a failure downloading
`https://formulae.brew.sh/api/internal/packages.arm64_tahoe.jws.json`, retried
silently, and was interrupted after roughly two minutes. Exit code was 130 and
no QEMU binary appeared.

## Private networking

- No `/opt/piglet`, Piglet launch daemon, runtime socket, network state, or log
  directory exists.
- No `socket_vmnet` or `socket_vmnet_client` binary was found.
- No `io.pgsty.piglet.vmnet` launchd service exists.
- No `10.10.10.0/24` route or address was observed.
- Existing host interfaces include the normal macOS `bridge0` and VPN `utun*`;
  no Piglet-owned interface exists.
- No privileged install/uninstall was attempted.

## Neighboring Pigsty source

- Path: `/Users/vonng/pgsty/pigsty`
- Commit: `dab5dba333a070d96fde1f9feb41761148f2be8c`
- Commit date/subject: 2026-08-14,
  `feat(cache): include Pigsty source in offline bundle`
- Pre-existing user change: `M vagrant/Vagrantfile` (not read as an authority
  and not modified)

Audited sources: `vagrant/config`, both provider templates, Makefile, SSH/DNS
scripts, all top-level `vagrant/spec/*.rb`, and matching Pigsty `conf/` files.
Effective behavior includes 128 GiB `/data` for ordinary nodes and four 32 GiB
data disks for all `minio*` names; `deci` and `simu` ignore scaling.

## Real E2E availability

- Current Mac can become the native macOS arm64/HVF M0 runner after QEMU,
  firmware, and a verified native image are available.
- Linux amd64/KVM native E2E is `blocked` by a missing runner.
- All VM boot, SSH, lifecycle, disk-grow, and network results are `not run`.
- Cross-builds are compile evidence only and have not yet run.

## Owner gates

- Image hosting/signing custody: not provided.
- Durable macOS self-hosted HVF runner ownership: not provided; current Mac is
  only confirmed for local development.
- HCP Vagrant migration/fallback schedule ownership: not provided; published
  deadlines are known from the PRD.

