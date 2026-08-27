#!/usr/bin/env bash
set -euo pipefail

# This is deliberately opt-in. It never downloads an image and mutates only a
# pipeline-owned copy under a system temporary directory. Required variables:
#   FARROW_IMAGE_PIPELINE_NATIVE_SOURCE      absolute canonical qcow2 path
#   FARROW_IMAGE_PIPELINE_NATIVE_SHA256      independently obtained digest
#   FARROW_IMAGE_PIPELINE_NATIVE_NAME        e.g. u24
#   FARROW_IMAGE_PIPELINE_NATIVE_RELEASE     immutable release
#   FARROW_IMAGE_PIPELINE_NATIVE_ARCH        amd64 or arm64
#   FARROW_IMAGE_PIPELINE_NATIVE_SOURCE_USER upstream bootstrap user
# Optional:
#   FARROW_IMAGE_PIPELINE_NATIVE_SOURCE_URI  immutable HTTPS URL or digest URN
#   FARROW_IMAGE_PIPELINE_NATIVE_LICENSE     SPDX expression (default NOASSERTION)

repo=$(cd "$(dirname "$0")/.." && pwd -P)
native_required=${FARROW_IMAGE_PIPELINE_NATIVE_REQUIRED:-0}
[[ ${native_required} == 0 || ${native_required} == 1 ]] || {
  printf 'FARROW_IMAGE_PIPELINE_NATIVE_REQUIRED must be 0 or 1\n' >&2
  exit 2
}

native_unavailable() {
  if [[ ${native_required} == 1 ]]; then
    printf 'FAIL native offline guest mutation NOT RUN: %s\n' "$1" >&2
    exit 1
  fi
  printf 'SKIP native offline guest mutation NOT RUN: %s\n' "$1"
  exit 0
}

required=(
  FARROW_IMAGE_PIPELINE_NATIVE_SOURCE
  FARROW_IMAGE_PIPELINE_NATIVE_SHA256
  FARROW_IMAGE_PIPELINE_NATIVE_NAME
  FARROW_IMAGE_PIPELINE_NATIVE_RELEASE
  FARROW_IMAGE_PIPELINE_NATIVE_ARCH
  FARROW_IMAGE_PIPELINE_NATIVE_SOURCE_USER
)
for variable in "${required[@]}"; do
  if [[ -z ${!variable:-} ]]; then
    native_unavailable 'set the documented FARROW_IMAGE_PIPELINE_NATIVE_* inputs'
  fi
done
for tool in python3 qemu-img virt-customize virt-cat; do
  if ! command -v "${tool}" >/dev/null; then
    native_unavailable "required tool missing: ${tool}"
  fi
done

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/farrow-image-pipeline-native.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/farrow-image-pipeline-native.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe native-test cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT
umask 077

source_uri=${FARROW_IMAGE_PIPELINE_NATIVE_SOURCE_URI:-urn:sha256:${FARROW_IMAGE_PIPELINE_NATIVE_SHA256}}
license=${FARROW_IMAGE_PIPELINE_NATIVE_LICENSE:-NOASSERTION}
"${repo}/packaging/image-pipeline/build.sh" \
  --mode offline \
  --source "${FARROW_IMAGE_PIPELINE_NATIVE_SOURCE}" \
  --expected-sha256 "${FARROW_IMAGE_PIPELINE_NATIVE_SHA256}" \
  --output "${temporary}/candidate" \
  --name "${FARROW_IMAGE_PIPELINE_NATIVE_NAME}" \
  --release "${FARROW_IMAGE_PIPELINE_NATIVE_RELEASE}" \
  --arch "${FARROW_IMAGE_PIPELINE_NATIVE_ARCH}" \
  --source-user "${FARROW_IMAGE_PIPELINE_NATIVE_SOURCE_USER}" \
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
