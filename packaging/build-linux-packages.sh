#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  printf 'usage: %s <version> [new-output-directory]\n' "$0" >&2
  exit 2
fi
version=$1
output=${2:-dist/linux-packages-${version}}
[[ ${version} =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || { printf 'invalid version: %s\n' "${version}" >&2; exit 2; }
if [[ ! ${SOURCE_DATE_EPOCH:-} =~ ^[0-9]+$ ]] || (( SOURCE_DATE_EPOCH <= 0 )); then
  printf 'SOURCE_DATE_EPOCH must be positive\n' >&2
  exit 2
fi
for tool in go nfpm syft jq shasum; do
  command -v "${tool}" >/dev/null || { printf 'required packaging tool is missing: %s\n' "${tool}" >&2; exit 3; }
done

repo=$(cd "$(dirname "$0")/.." && pwd -P)
"${repo}/packaging/check-toolchain.sh" packages
if [[ ${output} != /* ]]; then
  output=${repo}/${output}
fi
if [[ -e ${output} ]]; then
  [[ -d ${output} && -z $(find "${output}" -mindepth 1 -maxdepth 1 -print -quit) ]] || { printf 'refuse non-empty package output: %s\n' "${output}" >&2; exit 1; }
else
  install -d -m 0755 "${output}"
fi

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/piglet-linux-packages.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/piglet-linux-packages.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe package staging cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT

commit=${PIGLET_COMMIT:-uncommitted}
[[ ${commit} == uncommitted || ${commit} =~ ^[0-9a-f]{40}$ ]] || { printf 'PIGLET_COMMIT must be a full lowercase hash or uncommitted\n' >&2; exit 2; }
if date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
  build_date=$(date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)
else
  build_date=$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)
fi

for arch in amd64 arm64; do
  case ${arch} in
    amd64) deb_qemu=qemu-system-x86 ;;
    arm64) deb_qemu=qemu-system-arm ;;
  esac
  stage=${temporary}/${arch}
  install -d -m 0700 "${stage}"
  payload=${stage}/payload
  install -d -m 0755 "${payload}/usr/bin" "${payload}/opt/piglet/libexec" \
    "${payload}/usr/share/doc/piglet/licenses" "${payload}/usr/share/piglet/schemas"
  helper=${payload}/opt/piglet/libexec/piglet-hosts-helper
  binary=${payload}/usr/bin/piglet
  wrapper=${payload}/usr/bin/pigsty-vm
  (
    cd "${repo}"
    CGO_ENABLED=0 GOOS=linux GOARCH=${arch} GOFLAGS=-mod=readonly \
      go build -trimpath -buildvcs=false -ldflags '-buildid= -s -w' -o "${helper}" ./cmd/piglet-hosts-helper
  )
  helper_sha=$(shasum -a 256 "${helper}" | awk '{print $1}')
  ldflags="-buildid= -s -w -X github.com/pgsty/piglet/internal/version.Version=${version} -X github.com/pgsty/piglet/internal/version.Commit=${commit} -X github.com/pgsty/piglet/internal/version.Date=${build_date} -X github.com/pgsty/piglet/internal/hostconfig.ExpectedHelperSHA256=${helper_sha}"
  (
    cd "${repo}"
    CGO_ENABLED=0 GOOS=linux GOARCH=${arch} GOFLAGS=-mod=readonly \
      go build -trimpath -buildvcs=false -ldflags "${ldflags}" -o "${binary}" ./cmd/piglet
  )
  install -m 0755 "${repo}/packaging/pigsty/vm" "${wrapper}"
  chmod 0755 "${binary}" "${helper}" "${wrapper}"
  binary_sha=$(shasum -a 256 "${binary}" | awk '{print $1}')
  wrapper_sha256=$(shasum -a 256 "${wrapper}" | awk '{print $1}')
  wrapper_sha1=$(shasum "${wrapper}" | awk '{print $1}')
  wrapper_id=SPDXRef-File-usr-bin-pigsty-vm-${wrapper_sha256:0:16}
  build_info=${payload}/usr/share/doc/piglet/BUILD_INFO.json
  jq -n \
    --arg version "${version}" --arg commit "${commit}" --arg date "${build_date}" \
    --arg arch "${arch}" --arg binary_sha "${binary_sha}" --arg helper_sha "${helper_sha}" \
    --argjson source_epoch "${SOURCE_DATE_EPOCH}" \
    '{schema:1,version:$version,commit:$commit,date:$date,source_date_epoch:$source_epoch,goos:"linux",goarch:$arch,cgo_enabled:false,piglet_sha256:$binary_sha,hosts_helper_sha256:$helper_sha}' \
    >"${build_info}"
  chmod 0644 "${build_info}"
  install -m 0644 "${repo}/LICENSE" "${repo}/README.md" "${repo}/THIRD_PARTY_LICENSES.md" \
    "${repo}/docs/ARCHITECTURE.md" "${repo}/docs/IMAGE_CONTRACT.md" "${repo}/docs/INSTALL.md" \
    "${repo}/docs/MIGRATION.md" "${repo}/docs/NETWORKING.md" "${repo}/docs/RELEASE.md" \
    "${repo}/docs/SECURITY.md" "${repo}/docs/TESTING.md" "${repo}/docs/TROUBLESHOOTING.md" \
    "${repo}/docs/UPGRADE.md" "${payload}/usr/share/doc/piglet/"
  install -m 0644 "${repo}/schemas/piglet-v1.schema.json" "${payload}/usr/share/piglet/schemas/"
  install -m 0644 "${repo}"/third_party/licenses/* "${payload}/usr/share/doc/piglet/licenses/"

  for format in deb rpm; do
    package=${output}/piglet_${version}_linux_${arch}.${format}
    (
      cd "${repo}"
      NFPM_ARCH=${arch} PIGLET_VERSION=${version} PIGLET_PAYLOAD=${payload} PIGLET_DEB_QEMU=${deb_qemu} \
        nfpm package --config packaging/nfpm.yaml --packager "${format}" --target "${package}"
    )
    package_sha=$(shasum -a 256 "${package}" | awk '{print $1}')
    sbom=${package}.spdx.json
    package_name=$(basename "${package}")
    SYFT_CHECK_FOR_APP_UPDATE=false syft scan "dir:${payload}" --source-name "${package_name}" --source-version "${version}" \
      --output "spdx-json=${sbom}" >/dev/null
    jq --arg name "${package_name}" --arg version "${version}" --arg package_sha "${package_sha}" \
      --arg namespace "https://github.com/pgsty/piglet/sbom/${version}/${arch}/${format}/${package_sha}" \
      --arg created "${build_date}" --arg wrapper_sha256 "${wrapper_sha256}" \
      --arg wrapper_sha1 "${wrapper_sha1}" --arg wrapper_id "${wrapper_id}" '
      .name = $name |
      .documentNamespace = $namespace |
      .creationInfo.created = $created |
      (.packages[] | select(.name == $name)) |= (
        .versionInfo = $version |
        .supplier = "Organization: Pigsty" |
        .filesAnalyzed = false |
        .checksums = [{algorithm: "SHA256", checksumValue: $package_sha}] |
        .licenseConcluded = "Apache-2.0" |
        .licenseDeclared = "Apache-2.0" |
        .primaryPackagePurpose = "INSTALL"
      ) |
      if any(.files[]?; .fileName == "usr/bin/pigsty-vm") then . else
        (.packages[] | select(.name == $name) | .SPDXID) as $package_id |
        .files += [{fileName:"usr/bin/pigsty-vm",SPDXID:$wrapper_id,fileTypes:["APPLICATION","SOURCE"],
          checksums:[{algorithm:"SHA1",checksumValue:$wrapper_sha1},{algorithm:"SHA256",checksumValue:$wrapper_sha256}],
          licenseConcluded:"NOASSERTION",licenseInfoInFiles:["NOASSERTION"],copyrightText:"NOASSERTION"}] |
        .relationships += [{spdxElementId:$package_id,relatedSpdxElement:$wrapper_id,relationshipType:"CONTAINS"}]
      end' \
      "${sbom}" >"${sbom}.normalized"
    mv "${sbom}.normalized" "${sbom}"
    chmod 0644 "${package}" "${sbom}"
  done
done

(
  cd "${output}"
  shasum -a 256 piglet_* | LC_ALL=C sort >checksums.txt
)
chmod 0644 "${output}/checksums.txt"
printf 'built RPM/DEB packages and SPDX SBOMs in %s\n' "${output}"
