# Official image normalization candidate pipeline

This directory implements the repository-local portion of the PRD image
supply-chain gate. It accepts one already-downloaded immutable qcow2 plus an
independently obtained SHA-256. It never downloads an image, uploads an
artifact, changes a runtime/network file, marks an image `supported`, or reads
a signing key.

The default `validate` mode is useful on any host with Python 3 and
`qemu-img`. It safely copies and re-hashes the source, forces qcow2 parsing with
the same `qemu-img info --output=json -f qcow2` contract used by Farrow, checks
for a one-element backing chain, runs `qemu-img check`, and emits a reproducible
but explicitly unpublishable evidence bundle. **It does not mutate guest
credentials.** Both stdout and `validation.json` say `NATIVE MUTATION NOT RUN`.

The opt-in `offline` mode additionally requires libguestfs
`virt-customize` and `virt-cat`. It mutates only the staged copy, with
`--no-network`, and runs `normalize-guest.sh` in the offline appliance. The
script:

- fails if unrelated accounts occupy UID or GID 88;
- creates/normalizes `admin` GID 88 and `dba` UID 88, `/home/dba`, and bash;
- replaces password hashes with `!` for root, the source user, `dba`, and all
  normal users, and disables SSH password/root login;
- removes old `authorized_keys`, common private development keys and shell
  histories, SSH host keys, machine-id, random seed, and cloud-init cache;
- installs only the empty, locked `dba` account and its passwordless sudo rule;
- verifies those postconditions before writing a deterministic marker that the
  host reads back with `virt-cat`.

On SELinux guests the script runs `restorecon` over every path it creates or
replaces. The pipeline does not pass blanket `virt-customize
--selinux-relabel`, because that option is not valid for the Debian/Ubuntu
inputs; EL labeling is targeted in-guest and still covered by the required
native boot/readiness gate.

No first-boot cleanup is claimed: a publishable candidate must come from a
successful `offline` run. A validate-only bundle remains useful for source and
qcow2-policy review, but its manifest provenance starts with
`UNPUBLISHABLE VALIDATION-ONLY`.

## Inputs and bounded behavior

The source and output paths must be absolute. The source must be canonical,
regular, non-symlinked, at most 16 GiB, and stable while copied. The output
directory must not exist. The builder takes an exclusive adjacent lock, works
in a mode-0700 sibling temporary directory, verifies the digest during and
after copy, and publishes the complete directory with one rename. A failure
removes only its guarded temporary directory and never overwrites output.

The source URI defaults to `urn:sha256:<expected digest>`. If supplied, it must
be immutable HTTPS: moving `latest`, `current`, `release`, and `.latest.` paths
are rejected. The candidate URL follows the manifest's immutable HTTPS rule
and can contain one `{sha256}` placeholder, resolved after offline mutation.
It is provenance for a future owner upload; this pipeline performs no network
operation.

Example validation:

```bash
SOURCE_DATE_EPOCH=1787486400
SOURCE=/absolute/path/noble-server-cloudimg-amd64.img
SHA256=0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe

./packaging/image-pipeline/build.sh \
  --mode validate \
  --source "${SOURCE}" \
  --expected-sha256 "${SHA256}" \
  --output /absolute/new/path/u24-amd64-validation \
  --name u24 --release 20260801.0.0 --arch amd64 \
  --source-user ubuntu --boot uefi \
  --source-uri https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-amd64.img \
  --artifact-url 'https://images.example.invalid/u24/20260801.0.0/{sha256}.qcow2' \
  --license NOASSERTION \
  --source-date-epoch "${SOURCE_DATE_EPOCH}" \
  --manifest-version 2026082403
```

For native offline mutation, change `--mode validate` to `--mode offline`.
`virt-customize` and `virt-cat` can be overridden with absolute tool paths.
The tools receive a minimal environment, a temporary HOME/cache, fixed locale
and time zone, `SOURCE_DATE_EPOCH`, and no host credential variables.

## Outputs and reproducibility boundary

Every successful bundle contains exactly:

- the read-only qcow2 named by its resulting digest;
- `build-recipe.json` and the checksummed `normalize-guest.sh` recipe;
- an in-toto/SLSA v1 `provenance.intoto.json` statement;
- an SPDX 2.3 `sbom.spdx.json` artifact-boundary SBOM;
- a Farrow schema-1 `manifest-candidate.json`, always `status: testing`;
- `validation.json`, including before/after qemu facts and remaining gates;
- `checksums.txt`, covering every other file.

The SPDX file identifies the source and resulting qcow2. It deliberately does
not claim a guest package inventory; attach a package-level native guest SBOM
before promotion.

For fixed source bytes, recipe bytes, metadata, epoch, and tool versions,
validate mode is byte reproducible because the qcow2 is copied without
conversion and JSON is canonically ordered. Offline filesystem mutation is
bounded by the recorded libguestfs/QEMU versions and fixed epoch; release
evidence must build twice and compare the resulting `checksums.txt`. The
pipeline records the output digest rather than assuming native mutation is
reproducible without that comparison.

Signing is intentionally absent. The pipeline accepts no private key or KMS
reference and records `signing.performed: false`. If signature mechanics are
tested separately, only repository-labelled ephemeral/test keys may be used;
production active/standby custody remains an owner gate.

Run the secret-free contract tests with:

```bash
./tests/image-pipeline-test.sh
./tests/image-pipeline-native-test.sh   # prints an explicit SKIP unless opted in
```

The native test's required environment is documented in that script. Passing
the fake-boundary test is not evidence that libguestfs mutation ran.
