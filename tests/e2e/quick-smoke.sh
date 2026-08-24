#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 || $# -gt 4 ]]; then
  printf 'usage: %s <read-only-native-qcow2> <sha256> <new-output-root> [cycles]\n' "$0" >&2
  exit 2
fi

image=$1
expected_sha=$2
output_root=$3
cycles=${4:-1}

if [[ "${image}" != /* || "${output_root}" != /* ]]; then
  printf 'image and output root must be absolute paths\n' >&2
  exit 2
fi
if [[ ! "${expected_sha}" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'expected SHA-256 must be 64 lowercase hexadecimal characters\n' >&2
  exit 2
fi
if [[ ! "${cycles}" =~ ^[0-9]+$ ]] || (( cycles < 1 || cycles > 10 )); then
  printf 'cycles must be in range 1..10\n' >&2
  exit 2
fi
if [[ -e "${output_root}" ]]; then
  printf 'refuse existing output root: %s\n' "${output_root}" >&2
  exit 2
fi

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "${script_dir}/../.." && pwd)
binary=${PIGLET_M0_BINARY:-${repo_root}/bin/piglet-m0}
if [[ ! -x "${binary}" ]]; then
  printf 'M0 binary is not executable: %s\n' "${binary}" >&2
  exit 3
fi

mkdir -m 0700 "${output_root}"

for ((cycle = 1; cycle <= cycles; cycle++)); do
  cycle_name=$(printf 'cycle-%02d' "${cycle}")
  cycle_dir="${output_root}/${cycle_name}"
  artifact_dir="${cycle_dir}/artifacts"
  mkdir -m 0700 "${cycle_dir}"
  printf 'starting %s\n' "${cycle_name}"
  "${binary}" \
    --image "${image}" \
    --sha256 "${expected_sha}" \
    --work-dir "${artifact_dir}" \
    --ready-timeout 180s \
    >"${cycle_dir}/stdout.json" \
    2>"${cycle_dir}/stderr.log"
  jq -e '.events[-1].result == "passed" and .checks["runtime-residue"] == "none"' "${artifact_dir}/evidence.json" >/dev/null
  printf 'passed %s\n' "${cycle_name}"
done
