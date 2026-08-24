#!/usr/bin/env bash
set -euo pipefail

# This is deliberately opt-in. It never downloads an image and mutates only a
# pipeline-owned copy under a system temporary directory. Required variables:
#   PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE      absolute canonical qcow2 path
#   PIGLET_IMAGE_PIPELINE_NATIVE_SHA256      independently obtained digest
#   PIGLET_IMAGE_PIPELINE_NATIVE_NAME        e.g. u24
#   PIGLET_IMAGE_PIPELINE_NATIVE_RELEASE     immutable release
#   PIGLET_IMAGE_PIPELINE_NATIVE_ARCH        amd64 or arm64
#   PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE_USER upstream bootstrap user
# Optional:
#   PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE_URI  immutable HTTPS URL or digest URN
#   PIGLET_IMAGE_PIPELINE_NATIVE_LICENSE     SPDX expression (default NOASSERTION)

repo=$(cd "$(dirname "$0")/.." && pwd -P)
required=(
  PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE
  PIGLET_IMAGE_PIPELINE_NATIVE_SHA256
  PIGLET_IMAGE_PIPELINE_NATIVE_NAME
  PIGLET_IMAGE_PIPELINE_NATIVE_RELEASE
  PIGLET_IMAGE_PIPELINE_NATIVE_ARCH
  PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE_USER
)
for variable in "${required[@]}"; do
  if [[ -z ${!variable:-} ]]; then
    printf 'SKIP native offline guest mutation NOT RUN: set the documented PIGLET_IMAGE_PIPELINE_NATIVE_* inputs\n'
    exit 0
  fi
done
for tool in python3 qemu-img virt-customize virt-cat; do
  if ! command -v "${tool}" >/dev/null; then
    printf 'SKIP native offline guest mutation NOT RUN: required tool missing: %s\n' "${tool}"
    exit 0
  fi
done

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/piglet-image-pipeline-native.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/piglet-image-pipeline-native.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe native-test cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT
umask 077

source_uri=${PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE_URI:-urn:sha256:${PIGLET_IMAGE_PIPELINE_NATIVE_SHA256}}
license=${PIGLET_IMAGE_PIPELINE_NATIVE_LICENSE:-NOASSERTION}
"${repo}/packaging/image-pipeline/build.sh" \
  --mode offline \
  --source "${PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE}" \
  --expected-sha256 "${PIGLET_IMAGE_PIPELINE_NATIVE_SHA256}" \
  --output "${temporary}/candidate" \
  --name "${PIGLET_IMAGE_PIPELINE_NATIVE_NAME}" \
  --release "${PIGLET_IMAGE_PIPELINE_NATIVE_RELEASE}" \
  --arch "${PIGLET_IMAGE_PIPELINE_NATIVE_ARCH}" \
  --source-user "${PIGLET_IMAGE_PIPELINE_NATIVE_SOURCE_USER}" \
  --source-uri "${source_uri}" \
  --artifact-url 'https://images.example.invalid/native-test/{sha256}.qcow2' \
  --boot uefi --license "${license}" \
  --source-date-epoch 1787486400 --manifest-version 2026082403

python3 - "${temporary}/candidate/validation.json" <<'PY'
import json
import pathlib
import sys
validation = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert validation["native_mutation"]["status"] == "completed"
assert validation["native_mutation"]["marker"]["credential_hygiene"] == "applied"
assert validation["promotion"]["eligible"] is False
assert validation["signing"]["performed"] is False
PY
printf 'native offline guest mutation completed on a guarded copy; candidate remained unsigned/testing and was removed after verification\n'
