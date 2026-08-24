#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 <version> [output-directory]" >&2
  echo "set SOURCE_DATE_EPOCH and optionally PIGLET_COMMIT/PIGLET_RELEASE_BASE_URL" >&2
}

version=${1:-}
output=${2:-dist}
if [[ ! ${version} =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  usage
  exit 2
fi
if [[ ! ${SOURCE_DATE_EPOCH:-} =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be set to a positive Unix timestamp" >&2
  exit 2
fi
if (( SOURCE_DATE_EPOCH <= 0 )); then
  echo "SOURCE_DATE_EPOCH must be positive" >&2
  exit 2
fi
if [[ ${version} != *-* ]]; then
  echo "build-release.sh is development-only and requires a prerelease version" >&2
  exit 2
fi
release_base=${PIGLET_RELEASE_BASE_URL:-https://github.com/pgsty/piglet/releases/download/v${version}}
if [[ ! ${release_base} =~ ^https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9._~/%+@=-]*)?$ ]]; then
  echo "PIGLET_RELEASE_BASE_URL must be a narrowly encoded HTTPS URL" >&2
  exit 2
fi

repo=$(cd "$(dirname "$0")/.." && pwd -P)
"${repo}/packaging/check-toolchain.sh" go
if [[ ${output} != /* ]]; then
  output=${repo}/${output}
fi
if [[ -e ${output} ]]; then
  if [[ ! -d ${output} ]] || [[ -n $(find "${output}" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
    echo "refuse non-empty release output: ${output}" >&2
    exit 1
  fi
else
  install -d -m 0755 "${output}"
fi

commit=${PIGLET_COMMIT:-}
if [[ -z ${commit} ]]; then
  commit=$(git -C "${repo}" rev-parse --verify HEAD 2>/dev/null || true)
fi
if [[ -z ${commit} ]]; then
  commit=uncommitted
fi
if [[ ! ${commit} =~ ^([0-9a-f]{40}|uncommitted)$ ]]; then
  echo "PIGLET_COMMIT must be a 40-character lowercase Git hash or uncommitted" >&2
  exit 2
fi

if date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
  build_date=$(date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)
  touch_time=$(date -u -r "${SOURCE_DATE_EPOCH}" +%Y%m%d%H%M.%S)
else
  build_date=$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)
  touch_time=$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y%m%d%H%M.%S)
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/piglet-release.XXXXXX")
cleanup() {
  case ${temporary} in
    "${TMPDIR:-/tmp}"/piglet-release.*) rm -rf -- "${temporary}" ;;
    *) echo "refuse unsafe temporary cleanup: ${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT

ldflags="-buildid= -s -w -X github.com/pgsty/piglet/internal/version.Version=${version} -X github.com/pgsty/piglet/internal/version.Commit=${commit} -X github.com/pgsty/piglet/internal/version.Date=${build_date}"
targets=(darwin/arm64 darwin/amd64 linux/amd64 linux/arm64)

for target in "${targets[@]}"; do
  goos=${target%/*}
  goarch=${target#*/}
  root_name="piglet_${version}_${goos}_${goarch}"
  stage="${temporary}/${root_name}"
  install -d -m 0755 "${stage}/bin" "${stage}/docs" \
    "${stage}/schemas" "${stage}/third_party/licenses"
  (
    cd "${repo}"
    CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch} GOFLAGS=-mod=readonly \
      go build -trimpath -buildvcs=false -ldflags "${ldflags}" -o "${stage}/bin/piglet-hosts-helper" ./cmd/piglet-hosts-helper
  )
  helper_sha=$(shasum -a 256 "${stage}/bin/piglet-hosts-helper" | awk '{print $1}')
  (
    cd "${repo}"
    CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch} GOFLAGS=-mod=readonly \
      go build -trimpath -buildvcs=false -ldflags "${ldflags} -X github.com/pgsty/piglet/internal/hostconfig.ExpectedHelperSHA256=${helper_sha}" -o "${stage}/bin/piglet" ./cmd/piglet
  )
  chmod 0755 "${stage}/bin/piglet" "${stage}/bin/piglet-hosts-helper"
  piglet_sha=$(shasum -a 256 "${stage}/bin/piglet" | awk '{print $1}')
  install -m 0644 "${repo}/LICENSE" "${repo}/README.md" "${repo}/THIRD_PARTY_LICENSES.md" "${stage}/"
  install -m 0644 "${repo}/docs/ARCHITECTURE.md" "${repo}/docs/IMAGE_CONTRACT.md" "${repo}/docs/INSTALL.md" \
    "${repo}/docs/MIGRATION.md" "${repo}/docs/NETWORKING.md" "${repo}/docs/RELEASE.md" \
    "${repo}/docs/SECURITY.md" "${repo}/docs/TESTING.md" "${repo}/docs/TROUBLESHOOTING.md" \
    "${repo}/docs/UPGRADE.md" "${stage}/docs/"
  install -m 0644 "${repo}/schemas/piglet-v1.schema.json" "${stage}/schemas/"
  install -m 0644 "${repo}"/third_party/licenses/* "${stage}/third_party/licenses/"
  install -m 0755 "${repo}/packaging/pigsty/vm" "${stage}/bin/pigsty-vm"
  cat >"${stage}/BUILD_INFO.json" <<EOF
{
  "schema": 1,
  "version": "${version}",
  "commit": "${commit}",
  "date": "${build_date}",
  "goos": "${goos}",
  "goarch": "${goarch}",
  "cgo_enabled": false,
  "piglet_sha256": "${piglet_sha}",
  "hosts_helper_sha256": "${helper_sha}"
}
EOF
  chmod 0644 "${stage}/BUILD_INFO.json"
  find "${stage}" -exec touch -h -t "${touch_time}" {} +
  archive="${output}/${root_name}.tar.gz"
  (
    cd "${temporary}"
    find "${root_name}" -print | LC_ALL=C sort | \
      COPYFILE_DISABLE=1 tar --no-recursion --no-xattrs --no-acls --no-fflags --no-mac-metadata \
        --format ustar --uid 0 --gid 0 --uname root --gname root --numeric-owner -cf - -T -
  ) | gzip -n -9 >"${archive}"
  chmod 0644 "${archive}"
done

"${repo}/packaging/render-homebrew.sh" "${version}" "${release_base}" "${output}" "${output}/piglet.rb"

cat >"${output}/release.json" <<EOF
{
  "schema": 1,
  "version": "${version}",
  "commit": "${commit}",
  "date": "${build_date}",
  "source_date_epoch": ${SOURCE_DATE_EPOCH},
  "targets": ["darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"],
  "signed": false,
  "attested": false,
  "channel": "development"
}
EOF
chmod 0644 "${output}/release.json"

(
  cd "${output}"
  shasum -a 256 piglet_"${version}"_*.tar.gz piglet.rb release.json | LC_ALL=C sort >checksums.txt
)
chmod 0644 "${output}/checksums.txt"

echo "built Piglet ${version} development release in ${output}"
echo "commit=${commit} date=${build_date}"
