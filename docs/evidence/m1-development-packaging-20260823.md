# Development packaging — 2026-08-23

Result class: local packaging integration for the M1 development tarball and
Homebrew formula. This is not a published, signed, or attested release.

## Inputs and truth labels

```text
version:           0.1.0-dev.20260823
commit:            uncommitted
SOURCE_DATE_EPOCH: 1787486400
build date:        2026-08-23T12:00:00Z
Go toolchain:      1.26.7
```

The repository has no commit, so build metadata deliberately says
`uncommitted`; it does not invent provenance. `release.json` explicitly records
development channel, `signed: false`, and `attested: false`.

## Artifacts

Final output directory:

```text
/Users/vonng/pgsty/piglet/dist/controller-final-a
```

Archive SHA-256 values:

```text
darwin/arm64 6bec259ade90d01e80e72f228a0a7294f24c2c7546a99854ba503834b414a3a6
darwin/amd64 e13821da4bc05c4a5b889e0acc0f54b6d3a9647828660c4c34360f8c12f6b323
linux/arm64  1390b59d4cd6bb5d4cc148e3c157403c1954bead613b0bfb58e43b9da47897b7
linux/amd64  4bf8b3b5168779539484991802f4140dadc3b6c7a965b62dd9da47c136664790
```

Every archive contains only the version-stamped `piglet` binary, build info,
Apache license, third-party license inventory, selected safety/architecture
docs, and the v1 config schema. It excludes probe/staging binaries, project
state, keys, seeds, disks, and privileged network helpers. Linux binaries are
static ELF; Darwin binaries are native Mach-O for arm64/amd64.

## Verification

`packaging/verify-release.sh` passed checksum verification, path traversal and
secret-artifact exclusions, required-file checks, build metadata checks,
architecture inspection, Ruby syntax, and native version execution.

The first reproducibility probe used POSIX PAX and correctly failed because
macOS libarchive stored variable ctime and provenance xattrs. The builder was
changed to sorted metadata-stripped USTAR plus `gzip -n`. Two fresh builds
(`dist/repro-c` and `dist/repro-d`) then produced byte-identical archives,
checksums, formula, and release metadata. After the final M1 source changes,
`dist/controller-final-a` and `dist/controller-final-b` repeated the same
all-file identity gate after the composed private prepare/state/lease/start/
stop/rollback controller; the hashes above are from that exact current source.

The generated Homebrew formula has per-OS/per-architecture HTTPS URLs and
SHA-256 values, depends on QEMU, and installs only the unprivileged CLI. Current
`brew style` reports one file inspected and zero offenses. It was not installed
because the development URLs are intentionally not published yet.

After packaging, `make check` passed and `govulncheck ./...` again reported no
reachable vulnerabilities.

Homebrew formula/tap conventions were checked against:
<https://docs.brew.sh/Formula-Cookbook> and
<https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap>.

Remaining package/release gates are publication, production signing key
custody, checksums/signatures/attestation, GoReleaser, RPM/DEB, and native
Linux runtime evidence.
