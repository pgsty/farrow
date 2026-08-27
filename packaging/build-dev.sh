#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s <darwin|linux> <amd64|arm64> [output-directory]\n' "$0" >&2
}

if [[ $# -lt 2 || $# -gt 3 ]]; then
  usage
  exit 2
fi

goos=$1
goarch=$2
case ${goos} in darwin|linux) ;; *) usage; exit 2 ;; esac
case ${goarch} in amd64|arm64) ;; *) usage; exit 2 ;; esac

repo=$(cd "$(dirname "$0")/.." && pwd -P)
output=${3:-bin/${goos}_${goarch}}
if [[ ${output} != /* ]]; then
  output=${repo}/${output}
fi
case /${output}/ in */../*|*/./*)
  printf 'development output must not contain dot path segments: %s\n' "${output}" >&2
  exit 2
  ;;
esac
case ${output} in
  "${repo}"/bin|"${repo}"/bin/*) ;;
  *)
    printf 'development output must be bin or one of its subdirectories: %s\n' "${output}" >&2
    exit 2
    ;;
esac

for tool in awk chmod cmp date git go grep install ln mkdir mktemp mv rm rmdir; do
  command -v "${tool}" >/dev/null || {
    printf 'required development build tool is missing: %s\n' "${tool}" >&2
    exit 3
  }
done

bin_root=${repo}/bin
if [[ -L ${bin_root} || (-e ${bin_root} && ! -d ${bin_root}) ]]; then
  printf 'development bin root must be a real directory: %s\n' "${bin_root}" >&2
  exit 7
fi
install -d -m 0755 "${bin_root}"
resolved_bin_root=$(cd "${bin_root}" && pwd -P)
[[ ${resolved_bin_root} == "${bin_root}" ]] || {
  printf 'development bin root must not resolve through a symlink: %s\n' "${bin_root}" >&2
  exit 7
}
existing_parent=${output}
while [[ ! -e ${existing_parent} && ! -L ${existing_parent} ]]; do
  parent=${existing_parent%/*}
  [[ -n ${parent} && ${parent} != "${existing_parent}" ]] || {
    printf 'cannot resolve development output parent: %s\n' "${output}" >&2
    exit 7
  }
  existing_parent=${parent}
done
[[ -d ${existing_parent} ]] || {
  printf 'development output parent is not a directory: %s\n' "${existing_parent}" >&2
  exit 7
}
resolved_existing_parent=$(cd "${existing_parent}" && pwd -P)
case ${resolved_existing_parent} in
  "${resolved_bin_root}"|"${resolved_bin_root}"/*) ;;
  *) printf 'development output parent resolves outside bin: %s\n' "${resolved_existing_parent}" >&2; exit 7 ;;
esac
if [[ -L ${output} || (-e ${output} && ! -d ${output}) ]]; then
  printf 'development output must be a real directory: %s\n' "${output}" >&2
  exit 7
fi
if [[ ! -e ${output} ]]; then
  install -d -m 0755 "${output}"
fi
if [[ -L ${output} || ! -d ${output} ]]; then
  printf 'development output must be a real directory: %s\n' "${output}" >&2
  exit 7
fi
output=$(cd "${output}" && pwd -P)
case ${output} in
  "${resolved_bin_root}"|"${resolved_bin_root}"/*) ;;
  *) printf 'development output resolves outside bin: %s\n' "${output}" >&2; exit 7 ;;
esac
if command -v sha256sum >/dev/null 2>&1; then
  sha256_file() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  printf 'required development build tool is missing: sha256sum or shasum\n' >&2
  exit 3
fi

version=${FARROW_VERSION:-dev}
[[ ${version} =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]] || {
  printf 'FARROW_VERSION contains unsupported characters: %s\n' "${version}" >&2
  exit 2
}

commit=${FARROW_COMMIT:-}
if [[ -n ${commit} && ! ${commit} =~ ^[0-9a-f]{40}$ && ${commit} != uncommitted ]]; then
  printf 'FARROW_COMMIT must be a full lowercase Git hash or uncommitted\n' >&2
  exit 2
fi
if [[ -z ${commit} ]]; then
  commit=$(git -C "${repo}" rev-parse --verify HEAD 2>/dev/null || true)
  if [[ -n $(git -C "${repo}" status --short --untracked-files=normal 2>/dev/null) ]]; then
    commit=uncommitted
  fi
fi
[[ ${commit} =~ ^[0-9a-f]{40}$ ]] || commit=uncommitted

build_date=${FARROW_BUILD_DATE:-}
if [[ -z ${build_date} ]]; then
  build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
fi
[[ ${build_date} =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || {
  printf 'FARROW_BUILD_DATE must be an RFC3339 UTC timestamp\n' >&2
  exit 2
}

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/farrow-dev-build.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
release_stage=
link_stage=
cleanup() {
  if [[ -n ${release_stage} ]]; then
    case ${release_stage} in
      "${output}"/.farrow-release.next.*) rm -rf -- "${release_stage}" ;;
      *) printf 'refuse unsafe development release cleanup: %s\n' "${release_stage}" >&2 ;;
    esac
  fi
  if [[ -n ${link_stage} ]]; then
    case ${link_stage} in
      "${output}"/.farrow-links.next.*) rm -rf -- "${link_stage}" ;;
      *) printf 'refuse unsafe development link cleanup: %s\n' "${link_stage}" >&2 ;;
    esac
  fi
  case ${temporary} in
    "${temporary_parent}"/farrow-dev-build.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe development build cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT

helper=${temporary}/farrow-hosts-helper
binary=${temporary}/farrow
common_ldflags="-buildid= -X github.com/pgsty/farrow/internal/version.Version=${version} -X github.com/pgsty/farrow/internal/version.Commit=${commit} -X github.com/pgsty/farrow/internal/version.Date=${build_date}"

(
  cd "${repo}"
  CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch} GOFLAGS=-mod=readonly \
    go build -trimpath -buildvcs=false -ldflags "${common_ldflags}" \
      -o "${helper}" ./cmd/farrow-hosts-helper
)
helper_sha=$(sha256_file "${helper}")
(
  cd "${repo}"
  CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch} GOFLAGS=-mod=readonly \
    go build -trimpath -buildvcs=false \
      -ldflags "${common_ldflags} -X github.com/pgsty/farrow/internal/hostconfig.ExpectedHelperSHA256=${helper_sha}" \
      -o "${binary}" ./cmd/farrow
)
grep -a -F "${helper_sha}" "${binary}" >/dev/null || {
  printf 'built CLI does not contain the companion helper digest\n' >&2
  exit 1
}

binary_sha=$(sha256_file "${binary}")
releases=${output}/.farrow-releases
if [[ -L ${releases} || (-e ${releases} && ! -d ${releases}) ]]; then
  printf 'development release root is unsafe: %s\n' "${releases}" >&2
  exit 7
fi
if [[ ! -e ${releases} ]]; then
  mkdir -m 0700 "${releases}"
fi
if [[ -L ${releases} || ! -d ${releases} ]]; then
  printf 'development release root is unsafe: %s\n' "${releases}" >&2
  exit 7
fi
resolved_releases=$(cd "${releases}" && pwd -P)
[[ ${resolved_releases} == "${output}/.farrow-releases" && -O ${resolved_releases} ]] || {
  printf 'development release root resolves outside the owned output: %s\n' "${resolved_releases}" >&2
  exit 7
}
chmod 0700 "${resolved_releases}"
releases=${resolved_releases}
release_id=${goos}_${goarch}-${binary_sha:0:16}
release_root=${releases}/${release_id}
if [[ -e ${release_root} || -L ${release_root} ]]; then
  if [[ -L ${release_root} || ! -d ${release_root} || ! -O ${release_root} ]] ||
    ! cmp -s "${binary}" "${release_root}/farrow" ||
    ! cmp -s "${helper}" "${release_root}/farrow-hosts-helper"; then
    printf 'existing development release differs from the built pair: %s\n' "${release_root}" >&2
    exit 7
  fi
  chmod 0700 "${release_root}"
  chmod 0755 "${release_root}/farrow" "${release_root}/farrow-hosts-helper"
else
  release_stage=$(mktemp -d "${output}/.farrow-release.next.XXXXXX")
  chmod 0700 "${release_stage}"
  install -m 0755 "${binary}" "${release_stage}/farrow"
  install -m 0755 "${helper}" "${release_stage}/farrow-hosts-helper"
  cmp -s "${binary}" "${release_stage}/farrow"
  cmp -s "${helper}" "${release_stage}/farrow-hosts-helper"
  mv "${release_stage}" "${release_root}"
  release_stage=
fi

for name in farrow farrow-hosts-helper; do
  target=${output}/${name}
  if [[ -e ${target} || -L ${target} ]]; then
    if [[ -d ${target} || (! -f ${target} && ! -L ${target}) ]]; then
      printf 'refuse to replace a non-file development entry point: %s\n' "${target}" >&2
      exit 7
    fi
  fi
done
link_stage=$(mktemp -d "${output}/.farrow-links.next.XXXXXX")
chmod 0700 "${link_stage}"
for name in farrow farrow-hosts-helper; do
  ln -s ".farrow-releases/${release_id}/${name}" "${link_stage}/${name}"
done
# farrow resolves the companion beside its real versioned path, so publishing
# the CLI first cannot expose a mismatched helper even if this process stops.
for name in farrow farrow-hosts-helper; do
  mv -f "${link_stage}/${name}" "${output}/${name}"
done
rmdir "${link_stage}"
link_stage=

installed_helper_sha=$(sha256_file "${output}/farrow-hosts-helper")
[[ ${installed_helper_sha} == "${helper_sha}" ]]
grep -a -F "${installed_helper_sha}" "${output}/farrow" >/dev/null
printf 'built Farrow for %s/%s in %s\n' "${goos}" "${goarch}" "${output}"
printf 'helper sha256 %s\n' "${installed_helper_sha}"
