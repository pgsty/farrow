#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'usage: %s <version> <absolute-goreleaser-dist>\n' "$0" >&2
  exit 2
fi
version=$1
directory=$2
[[ ${version} =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || { printf 'invalid version: %s\n' "${version}" >&2; exit 2; }
[[ ${directory} == /* && -d ${directory} ]] || { printf 'GoReleaser dist must be an absolute directory\n' >&2; exit 2; }
if [[ ! ${SOURCE_DATE_EPOCH:-} =~ ^[0-9]+$ ]] || (( SOURCE_DATE_EPOCH <= 0 )); then
  printf 'SOURCE_DATE_EPOCH must be positive\n' >&2
  exit 2
fi
for tool in awk cmp date diff file find go grep jq sed shasum stat tar tr; do
  command -v "${tool}" >/dev/null || { printf 'required GoReleaser verification tool is missing: %s\n' "${tool}" >&2; exit 3; }
done
repo=$(cd "$(dirname "$0")/.." && pwd -P)
# shellcheck disable=SC1091
source "${repo}/packaging/toolchain.env"
if date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
  created=$(date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)
else
  created=$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)
fi

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/piglet-goreleaser-verify.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/piglet-goreleaser-verify.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe GoReleaser verification cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT

expected_paths=(
  LICENSE
  README.md
  THIRD_PARTY_LICENSES.md
  docs/ARCHITECTURE.md
  docs/IMAGE_CONTRACT.md
  docs/INSTALL.md
  docs/MIGRATION.md
  docs/NETWORKING.md
  docs/RELEASE.md
  docs/SECURITY.md
  docs/TESTING.md
  docs/TROUBLESHOOTING.md
  docs/UPGRADE.md
  schemas/piglet-v1.schema.json
  third_party/licenses/aead.dev-minisign-LICENSE
  third_party/licenses/github.com-diskfs-go-diskfs-LICENSE
  third_party/licenses/github.com-djherbis-times-LICENSE
  third_party/licenses/go.yaml.in-yaml-v3-LICENSE
  third_party/licenses/go.yaml.in-yaml-v3-NOTICE
  third_party/licenses/golang.org-go-stdlib-LICENSE
  third_party/licenses/golang.org-x-crypto-LICENSE
  third_party/licenses/golang.org-x-sys-LICENSE
  third_party/licenses/golang.org-x-term-LICENSE
  bin/piglet
  bin/piglet-hosts-helper
  bin/pigsty-vm
)
expected_list=$(printf '%s\n' "${expected_paths[@]}" | LC_ALL=C sort)

file_mode() {
  if stat -c '%a' "$1" >/dev/null 2>&1; then
    stat -c '%a' "$1"
  else
    stat -f '%Lp' "$1"
  fi
}

file_mtime() {
  if stat -c '%Y' "$1" >/dev/null 2>&1; then
    stat -c '%Y' "$1"
  else
    stat -f '%m' "$1"
  fi
}

(cd "${directory}" && shasum -a 256 -c checksums.txt)
checksum_names=$(awk '{print $2}' "${directory}/checksums.txt" | LC_ALL=C sort)
expected_checksum_names=$(
  for os_name in darwin linux; do
    for arch in amd64 arm64; do
      printf 'piglet_%s_%s_%s.tar.gz\n' "${version}" "${os_name}" "${arch}"
      printf 'piglet_%s_%s_%s.tar.gz.spdx.json\n' "${version}" "${os_name}" "${arch}"
    done
  done | LC_ALL=C sort
)
[[ ${checksum_names} == "${expected_checksum_names}" ]] || { printf 'unexpected GoReleaser checksum inventory\n' >&2; exit 1; }

host_os=$(uname -s | tr '[:upper:]' '[:lower:]')
host_arch=$(uname -m)
[[ ${host_arch} == x86_64 ]] && host_arch=amd64
[[ ${host_arch} == aarch64 ]] && host_arch=arm64
for os_name in darwin linux; do
  for arch in amd64 arm64; do
    archive_name=piglet_${version}_${os_name}_${arch}.tar.gz
    root_name=${archive_name%.tar.gz}
    archive=${directory}/${archive_name}
    sbom=${archive}.spdx.json
    [[ -s ${archive} && -s ${sbom} ]] || { printf 'missing archive/SBOM: %s\n' "${archive}" >&2; exit 1; }
    expected_archive_list=$(
      for path in "${expected_paths[@]}"; do
        printf '%s/%s\n' "${root_name}" "${path}"
      done | LC_ALL=C sort
    )
    actual_archive_list=$(tar -tzf "${archive}" | LC_ALL=C sort)
    [[ ${actual_archive_list} == "${expected_archive_list}" ]] || { printf 'unexpected raw archive member inventory in %s\n' "${archive}" >&2; diff -u <(printf '%s\n' "${expected_archive_list}") <(printf '%s\n' "${actual_archive_list}") >&2 || true; exit 1; }
    while IFS= read -r member; do
      [[ ${member} == "${root_name}" || ${member} == "${root_name}"/* ]]
      [[ ${member} != /* && ${member} != .. && ${member} != ../* && ${member} != */../* && ${member} != */.. && ${member} != *\\* ]] || { printf 'unsafe archive entry %q in %s\n' "${member}" "${archive}" >&2; exit 1; }
    done < <(tar -tzf "${archive}")
    if tar -tvzf "${archive}" | awk 'substr($1,1,1) != "-" {bad=1} END {exit bad}'; then
      :
    else
      printf 'archive contains a non-regular-file entry: %s\n' "${archive}" >&2
      exit 1
    fi
    extract=${temporary}/${os_name}-${arch}
    install -d -m 0700 "${extract}"
    tar -xzf "${archive}" -C "${extract}"
    root=${extract}/${root_name}
    [[ -z $(find "${root}" ! -type f ! -type d -print -quit) ]] || { printf 'archive contains a special entry: %s\n' "${archive}" >&2; exit 1; }
    actual_list=$(cd "${root}" && find . -type f -print | sed 's#^\./##' | LC_ALL=C sort)
    [[ ${actual_list} == "${expected_list}" ]] || { printf 'unexpected archive payload in %s\n' "${archive}" >&2; diff -u <(printf '%s\n' "${expected_list}") <(printf '%s\n' "${actual_list}") >&2 || true; exit 1; }
    [[ $(file_mode "${root}/bin/piglet") == 755 && $(file_mode "${root}/bin/piglet-hosts-helper") == 755 && $(file_mode "${root}/bin/pigsty-vm") == 755 ]]
    cmp "${repo}/packaging/pigsty/vm" "${root}/bin/pigsty-vm"
    for path in "${expected_paths[@]}"; do
      case ${path} in bin/piglet|bin/piglet-hosts-helper|bin/pigsty-vm) continue ;; esac
      [[ $(file_mode "${root}/${path}") == 644 ]] || { printf 'unexpected archive mode: %s/%s\n' "${archive}" "${path}" >&2; exit 1; }
    done
    for path in "${expected_paths[@]}"; do
      [[ $(file_mtime "${root}/${path}") == "${SOURCE_DATE_EPOCH}" ]] || { printf 'archive member mtime is not the fixed source epoch: %s/%s\n' "${archive}" "${path}" >&2; exit 1; }
    done
    case ${os_name}/${arch} in
      darwin/amd64) file_pattern='Mach-O 64-bit executable x86_64' ;;
      darwin/arm64) file_pattern='Mach-O 64-bit executable arm64' ;;
      linux/amd64) file_pattern='ELF 64-bit LSB executable, x86-64' ;;
      linux/arm64) file_pattern='ELF 64-bit LSB executable, ARM aarch64' ;;
    esac
    file -b "${root}/bin/piglet" | grep -F "${file_pattern}" >/dev/null
    file -b "${root}/bin/piglet-hosts-helper" | grep -F "${file_pattern}" >/dev/null
    go version -m "${root}/bin/piglet" | sed -n '1p' | grep -F "go${PIGLET_GO_VERSION}" >/dev/null
    go version -m "${root}/bin/piglet-hosts-helper" | sed -n '1p' | grep -F "go${PIGLET_GO_VERSION}" >/dev/null
    helper_sha=$(shasum -a 256 "${root}/bin/piglet-hosts-helper" | awk '{print $1}')
    grep -a -F "${helper_sha}" "${root}/bin/piglet" >/dev/null
    archive_sha=$(shasum -a 256 "${archive}" | awk '{print $1}')
    jq -e --arg name "${archive_name}" --arg created "${created}" --arg go_version "go${PIGLET_GO_VERSION}" \
      --arg namespace "https://github.com/pgsty/piglet/sbom/${archive_name}/${archive_sha}" '
      .spdxVersion == "SPDX-2.3" and .name == $name and
      .creationInfo.created == $created and .documentNamespace == $namespace and
      any(.packages[]?; .name == "go.yaml.in/yaml/v3") and
      any(.packages[]?; .name == "stdlib" and .versionInfo == $go_version and .licenseDeclared == "BSD-3-Clause") and
      ((.packages | length) > 0) and ((.files | length) > 0)
    ' "${sbom}" >/dev/null
    if [[ ${os_name} == "${host_os}" && ${arch} == "${host_arch}" ]]; then
      "${root}/bin/piglet" version | grep -F "piglet ${version}" >/dev/null
    fi
    printf '%s verified; helper=%s\n' "${archive_name}" "${helper_sha}"
  done
done
printf 'verified GoReleaser archives, paired helpers, SPDX documents, and checksums in %s\n' "${directory}"
