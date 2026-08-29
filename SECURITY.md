# Security

## Reporting a vulnerability

Report suspected vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/pgsty/farrow/security/advisories/new)
for this repository, or by email to <repo@pigsty.cc>. Please do not open a
public issue for an unfixed vulnerability.

Include the Farrow version (`farrow --json version`), the host platform, and the
smallest reproduction you have. You should get an acknowledgement within five
working days.

Farrow is pre-1.0. Fixes land in the next tagged release; there is no separate
backport line yet.

## Privilege boundary

Farrow runs unprivileged. It asks for administrator access for exactly two host
transactions, each announced before it happens:

1. **Installing the host-global fixed-IP network.** `farrow network install`
   prints the complete privileged plan and applies nothing without `--yes`.
   `farrow network uninstall` reverses it, restoring the recorded original
   ownership and mode of anything it changed.
2. **Installing the hosts helper.** `farrow-hosts-helper` is a separate,
   minimal, root-owned binary at `/opt/farrow/libexec/farrow-hosts-helper`. It
   is the only component that ever writes the system hosts file.

Everything else — image cache, deployment state, SSH material, QEMU processes —
is owned by the invoking user under `$FARROW_HOME` and never needs root.

### What the hosts helper will refuse

The helper is deliberately hard to misuse. It rejects any invocation that does
not satisfy every one of these:

- a target that is exactly the platform's native hosts path, and a staging path
  that is absolute;
- an effective UID of 0, with a target owned by `root:root`, unwritable by group
  and other, and with a link count of exactly one;
- a staging file with mode `0600`, a link count of one, and ownership matching
  `SUDO_UID`;
- a before-digest that still matches the target and an after-digest that matches
  the staging file, so a plan reviewed against a since-changed hosts file is
  refused rather than applied;
- a post-write re-read whose digest, ownership, link count, and mode all match
  what was promised.

The CLI only ever invokes a helper whose SHA-256 matches the digest compiled
into that exact Farrow build, so a mismatched or substituted helper is not run.

## Supply chain

- Release archives, Linux packages, SPDX SBOMs, the Homebrew formula, the
  installer, and the release metadata are all listed in `checksums.txt`.
- `checksums.txt` is signed with keyless Sigstore
  (`checksums.txt.sigstore.json`), and its SLSA provenance is attested
  (`checksums.provenance.sigstore.json`). The release workflow verifies both
  against its own GitHub Actions OIDC identity before publishing.
- Builds are reproducible: `-trimpath`, `-buildid=`, and `SOURCE_DATE_EPOCH`
  derived from the tagged commit.
- The Go toolchain, GoReleaser, nFPM, Syft, Cosign, Staticcheck, and
  govulncheck are all version-pinned in `packaging/toolchain.env` and verified
  at release time.

Verify a release before trusting it:

```bash
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/pgsty/farrow/.github/workflows/release.yml@refs/tags/v<version>" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
```

## Guest images

Guest images are distribution-owned artifacts pinned by SHA-256 in a catalog
signed with the keys embedded in the binary. Every fetch verifies the digest,
the byte count, the qcow2 structure, and the virtual size before the image is
published into the cache. Farrow refuses catalog upstream URLs that point at a
moving path such as `latest` or `current`.

Farrow does not patch or harden guest images. They are upstream cloud images,
and their contents are the upstream vendor's responsibility.
