# Official-image normalization pipeline — Go 1.27 source — 2026-08-24

Result class: **pipeline contract/rejection/reproducibility PASS; real qemu-img
validate PASS; native libguestfs mutation NOT RUN**.

The repository now contains a bounded local candidate builder under
`packaging/image-pipeline/`. It accepts an immutable local qcow2 plus an
independently supplied digest, performs a no-follow stable copy, forces qcow2
inspection and one-element backing-chain/check validation, and rejects
backing/data-file/encryption/unknown incompatible/extended-L2/corrupt/dirty
inputs. Output is published by one rename and never overwrites a path.

Validate-only mode is explicitly unpublishable and reports `NATIVE MUTATION
NOT RUN`. Opt-in offline mode uses `virt-customize --no-network`, removes old
authorized/private/host keys, locks passwords, clears machine/cloud-init
identity, creates `dba`/`admin` UID/GID 88, applies targeted SELinux labels,
and verifies a guest marker plus passwd/group postconditions. It still emits
only `status: testing` and accepts no signing key.

Every bundle contains the read-only digest-named qcow2, canonical recipe,
normalization script, validation, testing manifest candidate, artifact-boundary
SPDX 2.3, SLSA v1 in-toto provenance, and checksums. Guest package inventory,
repeat native builds, native boot smoke, owner hosting, and production signing
remain promotion gates.

## Verification

```text
shellcheck packaging/image-pipeline/build.sh \
  packaging/image-pipeline/normalize-guest.sh \
  tests/image-pipeline-test.sh tests/image-pipeline-native-test.sh
python3 -m py_compile packaging/image-pipeline/pipeline.py
./tests/image-pipeline-test.sh
./tests/image-pipeline-native-test.sh
```

ShellCheck and compilation passed without leaving pycache. The secret-free
suite passed deterministic two-run byte comparison, simulated offline boundary
and marker verification, and negative backing/data/encryption/incompatible/
extended/corrupt/dirty/raw/chain/check-error cases. The opt-in native test
printed `SKIP ... NOT RUN` because no explicit libguestfs source inputs were
provided; fake tools are not counted as native mutation.

A real QEMU 11.1 validate run created a synthetic plain 1 GiB qcow2 and passed
the complete forced-format info/backing-chain/check path. Its output said:

```text
NATIVE MUTATION NOT RUN
native_status=not-run-validation-only
eligible=false
format=qcow2
chain=true
```

The real bundle's `checksums.txt` SHA-256 was
`4f6f5ef626f24b0ae617922c6bdb7aae4f6ecfd754d6033d7b2b4b190071a238`;
all seven covered files verified. `validation.json` SHA-256 was
`f617a5da9bc50ac10660357c66550ec7f8d3ed320fd669f210bfffa3576f7176`.
The guarded temporary bundle was removed after these facts were recorded.
