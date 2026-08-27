#!/usr/bin/env bash
set -euo pipefail

for tool in cmp diff find go jq sed sort; do
  command -v "${tool}" >/dev/null || { printf 'required license verification tool is missing: %s\n' "${tool}" >&2; exit 3; }
done
repo=$(cd "$(dirname "$0")/.." && pwd -P)
# shellcheck disable=SC1091
source "${repo}/packaging/toolchain.env"
module_cache=$(go env GOMODCACHE)
go_root=$(go env GOROOT)
go_license=${go_root}/LICENSE
if [[ ! -f ${go_license} ]]; then
  go_license=$(cd "${go_root}/.." && pwd -P)/LICENSE
fi
[[ -f ${go_license} && ! -L ${go_license} ]] || { printf 'Go standard-library LICENSE is missing from the pinned toolchain\n' >&2; exit 1; }
[[ $(go env GOVERSION) == "go${FARROW_GO_VERSION}" ]] || { printf 'Go toolchain version differs from the license-review pin\n' >&2; exit 1; }
expected=$(printf '%s\n' \
  $'aead.dev/minisign\tv0.3.0' \
  $'github.com/diskfs/go-diskfs\tv1.9.4' \
  $'github.com/djherbis/times\tv1.6.0' \
  $'github.com/fsnotify/fsnotify\tv1.9.0' \
  $'github.com/go-viper/mapstructure/v2\tv2.4.0' \
  $'github.com/pelletier/go-toml/v2\tv2.2.4' \
  $'github.com/sagikazarmark/locafero\tv0.11.0' \
  $'github.com/sourcegraph/conc\tv0.3.1-0.20240121214520-5f936abd7ae8' \
  $'github.com/spf13/afero\tv1.15.0' \
  $'github.com/spf13/cast\tv1.10.0' \
  $'github.com/spf13/cobra\tv1.10.2' \
  $'github.com/spf13/pflag\tv1.0.10' \
  $'github.com/spf13/viper\tv1.21.0' \
  $'github.com/subosito/gotenv\tv1.6.0' \
  $'go.yaml.in/yaml/v3\tv3.0.5' \
  $'golang.org/x/crypto\tv0.52.0' \
  $'golang.org/x/sys\tv0.45.0' \
  $'golang.org/x/term\tv0.43.0' \
  $'golang.org/x/text\tv0.37.0' | LC_ALL=C sort)
actual=$(
  cd "${repo}"
  go list -deps -json ./cmd/farrow ./cmd/farrow-hosts-helper |
    jq -r 'select(.Module != null and .Module.Main != true) | [.Module.Path,.Module.Version] | @tsv' |
    LC_ALL=C sort -u
)
[[ ${actual} == "${expected}" ]] || { printf 'reachable module inventory differs\n' >&2; diff -u <(printf '%s\n' "${expected}") <(printf '%s\n' "${actual}") >&2 || true; exit 1; }

license_sources=(
  "aead.dev-minisign-LICENSE=${module_cache}/aead.dev/minisign@v0.3.0/LICENSE"
  "github.com-diskfs-go-diskfs-LICENSE=${module_cache}/github.com/diskfs/go-diskfs@v1.9.4/LICENSE"
  "github.com-djherbis-times-LICENSE=${module_cache}/github.com/djherbis/times@v1.6.0/LICENSE"
  "github.com-fsnotify-fsnotify-LICENSE=${module_cache}/github.com/fsnotify/fsnotify@v1.9.0/LICENSE"
  "github.com-go-viper-mapstructure-v2-LICENSE=${module_cache}/github.com/go-viper/mapstructure/v2@v2.4.0/LICENSE"
  "github.com-pelletier-go-toml-v2-LICENSE=${module_cache}/github.com/pelletier/go-toml/v2@v2.2.4/LICENSE"
  "github.com-sagikazarmark-locafero-LICENSE=${module_cache}/github.com/sagikazarmark/locafero@v0.11.0/LICENSE"
  "github.com-sourcegraph-conc-LICENSE=${module_cache}/github.com/sourcegraph/conc@v0.3.1-0.20240121214520-5f936abd7ae8/LICENSE"
  "github.com-spf13-afero-LICENSE=${module_cache}/github.com/spf13/afero@v1.15.0/LICENSE.txt"
  "github.com-spf13-cast-LICENSE=${module_cache}/github.com/spf13/cast@v1.10.0/LICENSE"
  "github.com-spf13-cobra-LICENSE=${module_cache}/github.com/spf13/cobra@v1.10.2/LICENSE.txt"
  "github.com-spf13-pflag-LICENSE=${module_cache}/github.com/spf13/pflag@v1.0.10/LICENSE"
  "github.com-spf13-viper-LICENSE=${module_cache}/github.com/spf13/viper@v1.21.0/LICENSE"
  "github.com-subosito-gotenv-LICENSE=${module_cache}/github.com/subosito/gotenv@v1.6.0/LICENSE"
  "go.yaml.in-yaml-v3-LICENSE=${module_cache}/go.yaml.in/yaml/v3@v3.0.5/LICENSE"
  "go.yaml.in-yaml-v3-NOTICE=${module_cache}/go.yaml.in/yaml/v3@v3.0.5/NOTICE"
  "golang.org-x-crypto-LICENSE=${module_cache}/golang.org/x/crypto@v0.52.0/LICENSE"
  "golang.org-x-sys-LICENSE=${module_cache}/golang.org/x/sys@v0.45.0/LICENSE"
  "golang.org-x-term-LICENSE=${module_cache}/golang.org/x/term@v0.43.0/LICENSE"
  "golang.org-x-text-LICENSE=${module_cache}/golang.org/x/text@v0.37.0/LICENSE"
  "golang.org-go-stdlib-LICENSE=${go_license}"
)
expected_license_names=()
for mapping in "${license_sources[@]}"; do
  name=${mapping%%=*}
  source=${mapping#*=}
  cmp "${repo}/third_party/licenses/${name}" "${source}"
  expected_license_names+=("${name}")
done
expected_license_list=$(printf '%s\n' "${expected_license_names[@]}" | LC_ALL=C sort)
actual_license_list=$(find "${repo}/third_party/licenses" -mindepth 1 -maxdepth 1 -type f -print | sed 's#^.*/##' | LC_ALL=C sort)
[[ ${actual_license_list} == "${expected_license_list}" ]] || {
  printf 'dependency license directory inventory differs\n' >&2
  diff -u <(printf '%s\n' "${expected_license_list}") <(printf '%s\n' "${actual_license_list}") >&2 || true
  exit 1
}
[[ -z $(find "${repo}/third_party/licenses" -mindepth 1 -maxdepth 1 ! -type f -print -quit) ]] || {
  printf 'dependency license directory contains a non-regular entry\n' >&2
  exit 1
}
printf 'verified nineteen reachable module versions, Go %s stdlib license, and exact upstream license/notice bytes\n' "${FARROW_GO_VERSION}"
