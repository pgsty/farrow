# M4 release/package mechanism evidence — 2026-08-24

> Historical Go 1.26.7 / pre-owner-scope artifact record. Current Go 1.27,
> 14-entry outputs are recorded in `m4-owner-scope-go127-20260824.md`.

Result class: reproducible packaging integration plus native Linux package
installation. This is **not** a production release, signature, attestation, or
v1.0 provenance claim. The real worktree has no publishable commit/remote/tag;
the GoReleaser run used an isolated synthetic Git repository only to execute
the release machinery without mutating repository history.

## Pinned toolchain

```text
Go           1.26.7
GoReleaser   2.16.0
nFPM         2.47.0
Syft         1.51.0
Cosign       3.1.3
Actionlint   1.7.12 (workflow validation only)
```

`packaging/check-toolchain.sh all`, ShellCheck 0.11.0, Actionlint, and
`goreleaser check` passed. The versions are checked in at
`packaging/toolchain.env`.

## GoReleaser archives and SPDX

The current source was copied without `.git`, `dist`, or binaries to a mode-0700
temporary root, committed and tagged only there, and given the non-contacting
remote URL `https://github.com/pgsty/piglet.git` so GoReleaser had real commit
metadata. Synthetic identity:

```text
commit: 6505da20673412492850d6fd75dd80e1cd099d43
SOURCE_DATE_EPOCH: 1787517344
snapshot version: 0.1.1-next
```

Two independent `goreleaser release --snapshot --clean --parallelism 1` runs
produced byte-identical `checksums.txt`, four wrapped archives, and four SPDX
JSON documents. `packaging/verify-goreleaser.sh` passed both sets. The final
hashes were:

```text
4375f95dabb0f1f4bde161f915cafc70ccdd0a702157fa77eede79d2ae58c88c  checksums.txt
949d1eef3e43df134361db7bc27f9e81942e46a2dbfc05189f0e06a8c7032873  piglet_0.1.1-next_darwin_amd64.tar.gz
86cb4bb64d8f06ae7c4b58fcbe12e2d788e96c5406ecf00b8257abe2fe914ef5  piglet_0.1.1-next_darwin_amd64.tar.gz.spdx.json
9ce7cff0f7a7b85e5b1f88f828e0c8e31f7202b73a2e18e7824e90c467a5a713  piglet_0.1.1-next_darwin_arm64.tar.gz
cc61fa000e886b9806e40fea310301dc525ecfa5599a4ebeea513552b676cadc  piglet_0.1.1-next_darwin_arm64.tar.gz.spdx.json
6e6e5574261b3e8d088a160f9c4676f5dc59ac4164b25030d7692120404f9528  piglet_0.1.1-next_linux_amd64.tar.gz
8ac2833129c7b4a807269cc7a359d68e8b058bb787c7b8f307c2456c92e98e21  piglet_0.1.1-next_linux_amd64.tar.gz.spdx.json
34589d45a228c9361c842b4ccc4d5f1f11d784265c9472d1e28f61c082fe63b3  piglet_0.1.1-next_linux_arm64.tar.gz
d84af85e6d824e6f81533fdcfa3bda78f0e98e8ac531a11c4300bd04c65a7e62  piglet_0.1.1-next_linux_arm64.tar.gz.spdx.json
```

Each archive is a single `piglet_<version>_<os>_<arch>/` tree with root/root
metadata, mode-0755 `bin/piglet` and `bin/piglet-hosts-helper`, the exact user
docs/schema, and six exact upstream license files. No PRD/prompt/evidence,
state, key, seed, disk, or staging file is present. The verifier recomputed each
helper SHA and found those exact bytes in the paired CLI:

```text
darwin/amd64 ccfbc25183e475a01691c5e01a687867edf12d3cc625d80378dac480b7108db7
darwin/arm64 09b531e7163a33dc507930dc40ba934117426e7a748973f1f985f9536dd12a81
linux/amd64  6f853dffb587c354cb67a2d97b70b97ff8bf961a37b3dc4da498e515c56c3d24
linux/arm64  f37f7e59b83fa09292cfbea9a712ffdc3bea221dae5597ccf92ec61c02e28ad8
```

The reproducibility gate first caught dynamic GoReleaser `.Date` injection and
correctly failed. It later caught concurrent completion reordering `piglet` and
the helper inside otherwise identical Darwin tarballs; release parallelism is
therefore fixed to 1. Using the Git commit date for binary/archive metadata,
root/root headers, content-derived SBOM namespaces, and the fixed source epoch
produced the final byte identity above. Intermediate failures also caught an
unavailable hook template field and a mistaken per-file `dst: docs`; none was
hidden or counted as a pass.

## RPM/DEB and payload SBOM

Two independent final builds of development version
`0.1.0-dev.20260824`, commit label `uncommitted`, and epoch `1787486400`
produced byte-identical packages, payload SPDX files, and checksums:

```text
b40dbde569bb0a9d2528e9aebc7788f085d9024588929ef49e3ff955f2bb135a  checksums.txt
2d79ef8a68b016f5f6ba4ed74e0fd43b54c61a10c1c9861280539085faaf6088  amd64.deb
f79179c495c6384c3fc219fa608af1f096ee6db5fd0d9228fbdccfd6b3235cae  amd64.rpm
f9aaa635c01c00a7aeb3e361c309efebc5a32042075c4e4985967625ac75435e  arm64.deb
ee1603c0e6f31fda7fea7acac6497d88c89710c346370d33ba6b3b80a6226807  arm64.rpm
```

Unlike the rejected first approach that cataloged only the package file, each
final SPDX scans the exact staged payload, contains both installed binaries,
the Piglet module and reachable dependencies (12 package records), the package
SHA-256, fixed creation time, and Apache-2.0 package declaration. The verifier
checks paths/types before extraction, exact payload/modes/architecture, DEB
metadata/recommendations, DEB/RPM byte parity, build info, and CLI/helper digest
pairing.

On Linux amd64 host `vonng-aimax` (Ubuntu 24.04), `--network none` disposable
containers used these existing images:

```text
ubuntu:24.04 image sha256:ef91e4b15da8323a1523adb2b371998dcd3063dae8553cc2744c178ccc065bc4
rockylinux/rockylinux:9.8.20260525.0 sha256:686e4558d0a5332a32cf8a4722fc4e3143bab0b6156e14792191161ae0bb78f4
```

Both installed the final amd64 package, verified metadata/recommendations,
root:root modes, all six license notices, exact version output, and
`dpkg -V`/`rpm -V`, then purged/erased with no `/usr/bin/piglet`, `/opt/piglet`,
`/usr/share/doc/piglet`, or `/usr/share/piglet` residue. The first RPM attempt
exposed unowned empty product directories; explicit RPM directory ownership was
added and the full test reran successfully. The host package database and fixed
paths were never modified; only containers were used, and remote staging was
removed.

## Development archives, final assembly, and signing mechanism

Two `build-release.sh` runs produced byte-identical four-target development
archives, formula, `release.json`, and a checksum manifest that now covers all
six release assets. The builder rejects stable versions and a malicious URL
such as `https://example.invalid";system("id");#` before creating output.
The final generated formula also passed `brew style` with one file inspected
and no offenses.

`assemble-release.sh` then combined matching GoReleaser archives/SPDX,
RPM/DEB/SPDX, formula, release metadata, and SLSA predicate into one 19-entry
manifest. Final development assembly hashes:

```text
0cdee798471b6a7e5c7d559e3e229a9834934b3f5a13ff3e83705578ab8b261d  checksums.txt
4de28576e2063e60e1f4c510dd145804b1dbc9dbcf8765fa5d4088b3c713cdfb  piglet.rb
cf80b4a2ab2ae82bd93829e65bc7c5d49013cc05a0e28e30393044fcc7095377  release.json
a584e2cd85097d3bc818035eb247ae0960e1f452de844806c7c1a6d0dc0c57d3  provenance-predicate.json
```

`test-cosign-roundtrip.sh` generated an encrypted ephemeral key in a mode-0700
system temporary directory, created and verified Cosign v3 signature and SLSA
provenance bundles, rejected a tampered checksum file through both verification
paths, and removed the key/bundles. No test or production private key remains.

`.github/workflows/release.yml` passed Actionlint. It verifies the exact tag and
commit, pins tools, builds/verifies every artifact, assembles the final manifest,
uses the exact GitHub OIDC workflow identity for keyless signing/attestation,
refuses overwrite, and creates only a draft release. It was **not run** and no
production release/identity/custody is claimed.

Verified development outputs retained for inspection are:

```text
dist/goreleaser-snapshot-20260824/       9 files
dist/release-assembly-snapshot-20260824/ 20 files (19 checksummed + manifest)
dist/linux-frozen-20260824-a/            RPM/DEB + payload SPDX
dist/dev-frozen-20260824-a/              development tarballs/formula/metadata
```

All temporary Git, signing-key, extraction, and remote package staging roots
were deleted after copying these checksum-verified artifacts. The retained
`dist` directories are ignored development outputs, not published GA assets.
