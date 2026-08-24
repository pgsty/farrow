#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  printf 'usage: %s <version> <commit-or-uncommitted> <source-epoch> <package-directory>\n' "$0" >&2
  exit 2
fi
version=$1
commit=$2
source_epoch=$3
directory=$4
[[ ${version} =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || { printf 'invalid version: %s\n' "${version}" >&2; exit 2; }
[[ ${commit} == uncommitted || ${commit} =~ ^[0-9a-f]{40}$ ]] || { printf 'expected commit must be a full lowercase hash or uncommitted\n' >&2; exit 2; }
if [[ ! ${source_epoch} =~ ^[0-9]+$ ]] || (( source_epoch <= 0 )); then
  printf 'expected source epoch must be positive\n' >&2
  exit 2
fi
[[ ${directory} == /* && -d ${directory} ]] || { printf 'package directory must be absolute\n' >&2; exit 2; }
for tool in awk bsdtar cmp date diff file find go grep jq sed shasum sort stat; do
  command -v "${tool}" >/dev/null || { printf 'required verification tool is missing: %s\n' "${tool}" >&2; exit 3; }
done
if date -u -r "${source_epoch}" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
  build_date=$(date -u -r "${source_epoch}" +%Y-%m-%dT%H:%M:%SZ)
else
  build_date=$(date -u -d "@${source_epoch}" +%Y-%m-%dT%H:%M:%SZ)
fi
debian_version=${version}
if [[ ${version} == *-* ]]; then
  debian_version=${version%%-*}~${version#*-}
fi
repo=$(cd "$(dirname "$0")/.." && pwd -P)
wrapper_sha=$(shasum -a 256 "${repo}/packaging/pigsty/vm" | awk '{print $1}')
# shellcheck disable=SC1091
source "${repo}/packaging/toolchain.env"

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/piglet-package-verify.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/piglet-package-verify.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe verification cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT

expected_paths=(
  opt/piglet/libexec/piglet-hosts-helper
  usr/bin/piglet
  usr/bin/pigsty-vm
  usr/share/doc/piglet/ARCHITECTURE.md
  usr/share/doc/piglet/BUILD_INFO.json
  usr/share/doc/piglet/IMAGE_CONTRACT.md
  usr/share/doc/piglet/INSTALL.md
  usr/share/doc/piglet/LICENSE
  usr/share/doc/piglet/MIGRATION.md
  usr/share/doc/piglet/NETWORKING.md
  usr/share/doc/piglet/README.md
  usr/share/doc/piglet/RELEASE.md
  usr/share/doc/piglet/SECURITY.md
  usr/share/doc/piglet/TESTING.md
  usr/share/doc/piglet/THIRD_PARTY_LICENSES.md
  usr/share/doc/piglet/TROUBLESHOOTING.md
  usr/share/doc/piglet/UPGRADE.md
  usr/share/doc/piglet/licenses/aead.dev-minisign-LICENSE
  usr/share/doc/piglet/licenses/github.com-diskfs-go-diskfs-LICENSE
  usr/share/doc/piglet/licenses/github.com-djherbis-times-LICENSE
  usr/share/doc/piglet/licenses/go.yaml.in-yaml-v3-LICENSE
  usr/share/doc/piglet/licenses/go.yaml.in-yaml-v3-NOTICE
  usr/share/doc/piglet/licenses/golang.org-go-stdlib-LICENSE
  usr/share/doc/piglet/licenses/golang.org-x-crypto-LICENSE
  usr/share/doc/piglet/licenses/golang.org-x-sys-LICENSE
  usr/share/doc/piglet/licenses/golang.org-x-term-LICENSE
  usr/share/piglet/schemas/piglet-v1.schema.json
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

verify_archive_member_mtimes() {
  local archive=$1
  local root=$2
  local label=$3
  local expected_mtime=$4
  local member normalized path
  while IFS= read -r member; do
    normalized=${member}
    while [[ ${normalized} == ./* ]]; do normalized=${normalized#./}; done
    normalized=${normalized#/}
    normalized=${normalized%/}
    [[ -n ${normalized} ]] || continue
    path=${root}/${normalized}
    [[ -e ${path} && ! -L ${path} ]] || { printf 'archive member was not extracted as a regular path: %s (%s)\n' "${member}" "${label}" >&2; exit 1; }
    [[ $(file_mtime "${path}") == "${expected_mtime}" ]] || {
      printf 'package member mtime differs from canonical value %s: %s (%s)\n' "${expected_mtime}" "${member}" "${label}" >&2
      exit 1
    }
  done < <(bsdtar -tf "${archive}")
}

extract_package() {
  local package=$1
  local format=$2
  local root=$3
  local container
  local -a data_archives
  install -d -m 0700 "${root}"
  if [[ ${format} == deb ]]; then
    container=${root}-container
    install -d -m 0700 "${container}"
    local control_count=0 data_count=0 binary_count=0 member
    while IFS= read -r member; do
      case ${member} in
        debian-binary) ((binary_count += 1)) ;;
        control.tar.*) ((control_count += 1)) ;;
        data.tar.*) ((data_count += 1)) ;;
        *) printf 'unsafe DEB container member %q in %s\n' "${member}" "${package}" >&2; exit 1 ;;
      esac
    done < <(bsdtar -tf "${package}")
    [[ ${binary_count} -eq 1 && ${control_count} -eq 1 && ${data_count} -eq 1 ]] || { printf 'unexpected DEB container inventory: %s\n' "${package}" >&2; exit 1; }
    bsdtar -xf "${package}" -C "${container}"
    verify_archive_member_mtimes "${package}" "${container}" "${package} outer container" "${source_epoch}"
    data_archives=("${container}"/data.tar.*)
    [[ ${#data_archives[@]} -eq 1 && -f ${data_archives[0]} ]] || { printf 'DEB must contain exactly one data archive: %s\n' "${package}" >&2; exit 1; }
    verify_payload_archive "${data_archives[0]}" "${package}"
    bsdtar -xf "${data_archives[0]}" -C "${root}"
    verify_archive_member_mtimes "${data_archives[0]}" "${root}" "${package} payload" "${source_epoch}"
  else
    verify_payload_archive "${package}" "${package}"
    bsdtar -xf "${package}" -C "${root}"
    # nFPM canonicalizes RPM cpio member mtimes to the Unix epoch. Binding that
    # format-specific zero is deterministic; BUILD_INFO separately binds the
    # requested source epoch and its derived UTC build date.
    verify_archive_member_mtimes "${package}" "${root}" "${package} payload" 0
  fi
}

verify_payload_archive() {
  local archive=$1
  local package=$2
  local member normalized expected allowed
  if ! bsdtar -tvf "${archive}" | awk 'substr($1, 1, 1) != "-" && substr($1, 1, 1) != "d" {bad=1} END {exit bad}'; then
    printf 'package payload contains a non-file/non-directory entry: %s\n' "${package}" >&2
    exit 1
  fi
  while IFS= read -r member; do
    normalized=${member}
    while [[ ${normalized} == ./* ]]; do normalized=${normalized#./}; done
    normalized=${normalized#/}
    normalized=${normalized%/}
    [[ -n ${normalized} && ${normalized} != .. && ${normalized} != ../* && ${normalized} != */../* && ${normalized} != */.. && ${normalized} != *\\* ]] || { printf 'unsafe payload path %q in %s\n' "${member}" "${package}" >&2; exit 1; }
    allowed=false
    for expected in "${expected_paths[@]}"; do
      if [[ ${normalized} == "${expected}" || ${expected} == "${normalized}"/* ]]; then
        allowed=true
        break
      fi
    done
    [[ ${allowed} == true ]] || { printf 'unexpected payload path %q in %s\n' "${member}" "${package}" >&2; exit 1; }
  done < <(bsdtar -tf "${archive}")
}

verify_deb_control() {
  local package=$1
  local arch=$2
  local container control control_root recommends dependency deb_qemu
  local -a control_archives
  container=${temporary}/control-${arch}
  install -d -m 0700 "${container}"
  bsdtar -xf "${package}" -C "${container}"
  control_archives=("${container}"/control.tar.*)
  [[ ${#control_archives[@]} -eq 1 && -f ${control_archives[0]} ]] || { printf 'DEB must contain exactly one control archive: %s\n' "${package}" >&2; exit 1; }
  control_root=${container}/contents
  install -d -m 0700 "${control_root}"
  bsdtar -xf "${control_archives[0]}" -C "${control_root}"
  verify_archive_member_mtimes "${control_archives[0]}" "${control_root}" "${package} control" "${source_epoch}"
  control=${control_root}/control
  [[ -f ${control} && ! -L ${control} ]] || { printf 'DEB control file is missing or unsafe: %s\n' "${package}" >&2; exit 1; }
  grep -Fx 'Package: piglet' "${control}" >/dev/null
  grep -Fx "Version: ${debian_version}-1" "${control}" >/dev/null
  grep -Fx "Architecture: ${arch}" "${control}" >/dev/null
  grep -Fx 'Maintainer: Pigsty <repo@pigsty.cc>' "${control}" >/dev/null
  case ${arch} in
    amd64) deb_qemu=qemu-system-x86 ;;
    arm64) deb_qemu=qemu-system-arm ;;
  esac
  recommends=$(sed -n 's/^Recommends: //p' "${control}")
  for dependency in "${deb_qemu}" qemu-utils openssh-client iproute2; do
    [[ ", ${recommends}," == *", ${dependency},"* ]] || { printf 'DEB lacks recommended dependency %s: %s\n' "${dependency}" "${package}" >&2; exit 1; }
  done
}

(cd "${directory}" && shasum -a 256 -c checksums.txt)
checksum_names=$(awk '{print $2}' "${directory}/checksums.txt" | LC_ALL=C sort)
expected_checksum_names=$(
  for arch in amd64 arm64; do
    for format in deb rpm; do
      printf 'piglet_%s_linux_%s.%s\n' "${version}" "${arch}" "${format}"
      printf 'piglet_%s_linux_%s.%s.spdx.json\n' "${version}" "${arch}" "${format}"
    done
  done | LC_ALL=C sort
)
[[ ${checksum_names} == "${expected_checksum_names}" ]] || { printf 'unexpected Linux package checksum inventory\n' >&2; exit 1; }
for arch in amd64 arm64; do
  case ${arch} in
    amd64) architecture_pattern='x86-64' ;;
    arm64) architecture_pattern='ARM aarch64' ;;
  esac
  for format in deb rpm; do
    package=${directory}/piglet_${version}_linux_${arch}.${format}
    sbom=${package}.spdx.json
    [[ -s ${package} && -s ${sbom} ]] || { printf 'missing package/SBOM: %s\n' "${package}" >&2; exit 1; }
    package_sha=$(shasum -a 256 "${package}" | awk '{print $1}')
    namespace=https://github.com/pgsty/piglet/sbom/${version}/${arch}/${format}/${package_sha}
    jq -e --arg name "$(basename "${package}")" --arg version "${version}" --arg digest "${package_sha}" \
      --arg created "${build_date}" --arg namespace "${namespace}" --arg go_version "go${PIGLET_GO_VERSION}" --arg wrapper_sha "${wrapper_sha}" '
      .spdxVersion == "SPDX-2.3" and
      .name == $name and
      .creationInfo.created == $created and .documentNamespace == $namespace and
      any(.packages[]?; .name == $name and .versionInfo == $version and any(.checksums[]?; .algorithm == "SHA256" and .checksumValue == $digest)) and
      any(.packages[]?; .name == "github.com/pgsty/piglet") and
      any(.packages[]?; .name == "github.com/diskfs/go-diskfs") and
      any(.packages[]?; .name == "go.yaml.in/yaml/v3") and
      any(.packages[]?; .name == "stdlib" and .versionInfo == $go_version and .licenseDeclared == "BSD-3-Clause") and
      any(.files[]?; .fileName == "usr/bin/piglet") and
      any(.files[]?; .fileName == "usr/bin/pigsty-vm" and any(.checksums[]?; .algorithm == "SHA256" and .checksumValue == $wrapper_sha)) and
      any(.files[]?; .fileName == "opt/piglet/libexec/piglet-hosts-helper") and
      ((.packages | length) >= 10) and ((.files | length) >= 3)
    ' "${sbom}" >/dev/null

    root=${temporary}/${arch}-${format}
    extract_package "${package}" "${format}" "${root}"
    [[ -z $(find "${root}" -type l -print -quit) ]] || { printf 'package contains a symbolic link: %s\n' "${package}" >&2; exit 1; }
    actual_list=$(cd "${root}" && find . -type f -print | sed 's#^\./##' | LC_ALL=C sort)
    [[ ${actual_list} == "${expected_list}" ]] || { printf 'unexpected package payload in %s\n' "${package}" >&2; diff -u <(printf '%s\n' "${expected_list}") <(printf '%s\n' "${actual_list}") >&2 || true; exit 1; }
    [[ $(file_mode "${root}/usr/bin/piglet") == 755 ]]
    [[ $(file_mode "${root}/opt/piglet/libexec/piglet-hosts-helper") == 755 ]]
    [[ $(file_mode "${root}/usr/bin/pigsty-vm") == 755 ]]
    cmp "${repo}/packaging/pigsty/vm" "${root}/usr/bin/pigsty-vm"
    for path in opt/piglet opt/piglet/libexec usr/share/doc/piglet usr/share/doc/piglet/licenses usr/share/piglet usr/share/piglet/schemas; do
      [[ -d ${root}/${path} && $(file_mode "${root}/${path}") == 755 ]] || { printf 'unexpected package directory mode for %s in %s\n' "${path}" "${package}" >&2; exit 1; }
    done
    for path in "${expected_paths[@]}"; do
      case ${path} in opt/piglet/libexec/piglet-hosts-helper|usr/bin/piglet|usr/bin/pigsty-vm) continue ;; esac
      [[ $(file_mode "${root}/${path}") == 644 ]] || { printf 'unexpected mode for %s in %s\n' "${path}" "${package}" >&2; exit 1; }
    done
    file -b "${root}/usr/bin/piglet" | grep -F "${architecture_pattern}" >/dev/null
    file -b "${root}/opt/piglet/libexec/piglet-hosts-helper" | grep -F "${architecture_pattern}" >/dev/null
    go version -m "${root}/usr/bin/piglet" | sed -n '1p' | grep -F "go${PIGLET_GO_VERSION}" >/dev/null
    go version -m "${root}/opt/piglet/libexec/piglet-hosts-helper" | sed -n '1p' | grep -F "go${PIGLET_GO_VERSION}" >/dev/null
    piglet_sha=$(shasum -a 256 "${root}/usr/bin/piglet" | awk '{print $1}')
    helper_sha=$(shasum -a 256 "${root}/opt/piglet/libexec/piglet-hosts-helper" | awk '{print $1}')
    grep -a -F "${helper_sha}" "${root}/usr/bin/piglet" >/dev/null
    grep -a -F "${commit}" "${root}/usr/bin/piglet" >/dev/null
    grep -a -F "${build_date}" "${root}/usr/bin/piglet" >/dev/null
    jq -e --arg version "${version}" --arg arch "${arch}" --arg commit "${commit}" --arg date "${build_date}" \
      --argjson source_epoch "${source_epoch}" --arg piglet_sha "${piglet_sha}" --arg helper_sha "${helper_sha}" '
      .schema == 1 and .version == $version and .goos == "linux" and .goarch == $arch and
      .commit == $commit and .date == $date and .source_date_epoch == $source_epoch and
      .cgo_enabled == false and .piglet_sha256 == $piglet_sha and .hosts_helper_sha256 == $helper_sha
    ' "${root}/usr/share/doc/piglet/BUILD_INFO.json" >/dev/null
    if [[ ${format} == deb ]]; then
      verify_deb_control "${package}" "${arch}"
    fi
  done
  for path in "${expected_paths[@]}"; do
    cmp "${temporary}/${arch}-deb/${path}" "${temporary}/${arch}-rpm/${path}"
  done
done
printf 'verified checksums, SPDX, metadata, payload, modes, architecture, and DEB/RPM parity in %s\n' "${directory}"
