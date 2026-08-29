#!/usr/bin/env bash
set -euo pipefail

script_directory=$(cd "$(dirname "$0")" && pwd -P)
# shellcheck disable=SC1091
source "${script_directory}/semver.sh"

if [[ $# -ne 6 ]]; then
  printf 'usage: %s <version> <commit> <source-epoch> <goreleaser-dist> <linux-package-dir> <new-output-dir>\n' "$0" >&2
  exit 2
fi
version=$1
commit=$2
source_epoch=$3
goreleaser_dist=$4
package_directory=$5
output=$6
if ! farrow_is_semver "${version}" || [[ ! ${commit} =~ ^[0-9a-f]{40}$ || ! ${source_epoch} =~ ^[0-9]+$ ]] || (( source_epoch <= 0 )); then
  printf 'invalid release identity\n' >&2
  exit 2
fi
[[ ${goreleaser_dist} == /* && -d ${goreleaser_dist} && ${package_directory} == /* && -d ${package_directory} && ${output} == /* ]] || { printf 'release input/output paths must be absolute\n' >&2; exit 2; }
[[ ! -e ${output} ]] || { printf 'release output must not already exist: %s\n' "${output}" >&2; exit 1; }
for tool in jq shasum; do
  command -v "${tool}" >/dev/null || { printf 'required release assembly tool is missing: %s\n' "${tool}" >&2; exit 3; }
done
repo=$(cd "$(dirname "$0")/.." && pwd -P)
# shellcheck disable=SC1091
source "${repo}/packaging/toolchain.env"
if date -u -r "${source_epoch}" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
  build_date=$(date -u -r "${source_epoch}" +%Y-%m-%dT%H:%M:%SZ)
else
  build_date=$(date -u -d "@${source_epoch}" +%Y-%m-%dT%H:%M:%SZ)
fi
signed=${FARROW_RELEASE_SIGNED:-false}
attested=${FARROW_RELEASE_ATTESTED:-false}
[[ ${signed} == true || ${signed} == false ]]
[[ ${attested} == true || ${attested} == false ]]
channel=stable
farrow_is_stable_release "${version}" || channel=prerelease
install -d -m 0755 "${output}"

for os_name in darwin linux; do
  for arch in amd64 arm64; do
    archive=farrow_${version}_${os_name}_${arch}.tar.gz
    for source in "${goreleaser_dist}/${archive}" "${goreleaser_dist}/${archive}.spdx.json"; do
      [[ -s ${source} && ! -L ${source} ]] || { printf 'missing GoReleaser asset: %s\n' "${source}" >&2; exit 1; }
      install -m 0644 "${source}" "${output}/"
    done
  done
done
for arch in amd64 arm64; do
  for format in deb rpm; do
    package=farrow_${version}_linux_${arch}.${format}
    for source in "${package_directory}/${package}" "${package_directory}/${package}.spdx.json"; do
      [[ -s ${source} && ! -L ${source} ]] || { printf 'missing Linux package asset: %s\n' "${source}" >&2; exit 1; }
      install -m 0644 "${source}" "${output}/"
    done
  done
done

release_base=${FARROW_RELEASE_BASE_URL:-https://github.com/pgsty/farrow/releases/download/v${version}}
"${repo}/packaging/render-homebrew.sh" "${version}" "${release_base}" "${output}" "${output}/farrow.rb"
install -m 0755 "${repo}/packaging/install.sh" "${output}/install.sh"
jq -n \
  --arg version "${version}" --arg commit "${commit}" --arg date "${build_date}" --arg channel "${channel}" \
  --arg go "${FARROW_GO_VERSION}" --arg goreleaser "${FARROW_GORELEASER_VERSION}" \
  --arg nfpm "${FARROW_GORELEASER_NFPM_VERSION}" --arg nfpm_standalone "${FARROW_NFPM_VERSION}" \
  --arg syft "${FARROW_SYFT_VERSION}" --arg cosign "${FARROW_COSIGN_VERSION}" \
  --arg staticcheck "${FARROW_STATICCHECK_VERSION}" --arg govulncheck "${FARROW_GOVULNCHECK_VERSION}" \
  --argjson epoch "${source_epoch}" --argjson signed "${signed}" --argjson attested "${attested}" \
  '{schema:1,version:$version,commit:$commit,date:$date,source_date_epoch:$epoch,channel:$channel,
    targets:["darwin/amd64","darwin/arm64","linux/amd64","linux/arm64"],
    packages:["deb/amd64","deb/arm64","rpm/amd64","rpm/arm64"],
    toolchain:{go:$go,goreleaser:$goreleaser,nfpm:$nfpm,nfpm_engine:"goreleaser-embedded",nfpm_standalone:$nfpm_standalone,syft:$syft,cosign:$cosign,staticcheck:$staticcheck,govulncheck:$govulncheck},
    signed:$signed,attested:$attested}' >"${output}/release.json"
chmod 0644 "${output}/release.json"

builder_id=${FARROW_BUILDER_ID:-https://github.com/pgsty/farrow/packaging/assemble-release.sh}
invocation_id=${FARROW_INVOCATION_ID:-local-development-assembly}
jq -n --arg version "${version}" --arg commit "${commit}" \
  --arg builder "${builder_id}" --arg invocation "${invocation_id}" \
  '{buildDefinition:{buildType:"https://github.com/pgsty/farrow/release/v1",
      externalParameters:{version:$version},internalParameters:{},
      resolvedDependencies:[{uri:"git+https://github.com/pgsty/farrow",digest:{gitCommit:$commit}}]},
    runDetails:{builder:{id:$builder},metadata:{invocationId:$invocation}}}' \
  >"${output}/provenance-predicate.json"
chmod 0644 "${output}/provenance-predicate.json"

(
  cd "${output}"
  shasum -a 256 farrow_* farrow.rb install.sh release.json provenance-predicate.json | LC_ALL=C sort >checksums.txt
)
chmod 0644 "${output}/checksums.txt"
[[ $(wc -l <"${output}/checksums.txt" | tr -d ' ') -eq 20 ]] || { printf 'unexpected final release asset count\n' >&2; exit 1; }
printf 'assembled 20 checksummed release assets in %s\n' "${output}"
