#!/usr/bin/env bash
set -euo pipefail

for tool in cmp diff go jq sort; do
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
[[ $(go env GOVERSION) == "go${PIGLET_GO_VERSION}" ]] || { printf 'Go toolchain version differs from the license-review pin\n' >&2; exit 1; }
expected=$(printf '%s\n' \
  $'aead.dev/minisign\tv0.3.0' \
  $'github.com/diskfs/go-diskfs\tv1.9.4' \
  $'github.com/djherbis/times\tv1.6.0' \
  $'go.yaml.in/yaml/v3\tv3.0.5' \
  $'golang.org/x/crypto\tv0.52.0' \
  $'golang.org/x/sys\tv0.45.0' \
  $'golang.org/x/term\tv0.43.0' | LC_ALL=C sort)
actual=$(
  cd "${repo}"
  go list -deps -json ./cmd/piglet ./cmd/piglet-hosts-helper |
    jq -r 'select(.Module != null and .Module.Main != true) | [.Module.Path,.Module.Version] | @tsv' |
    LC_ALL=C sort -u
)
[[ ${actual} == "${expected}" ]] || { printf 'reachable module inventory differs\n' >&2; diff -u <(printf '%s\n' "${expected}") <(printf '%s\n' "${actual}") >&2 || true; exit 1; }

cmp "${repo}/third_party/licenses/aead.dev-minisign-LICENSE" "${module_cache}/aead.dev/minisign@v0.3.0/LICENSE"
cmp "${repo}/third_party/licenses/github.com-diskfs-go-diskfs-LICENSE" "${module_cache}/github.com/diskfs/go-diskfs@v1.9.4/LICENSE"
cmp "${repo}/third_party/licenses/github.com-djherbis-times-LICENSE" "${module_cache}/github.com/djherbis/times@v1.6.0/LICENSE"
cmp "${repo}/third_party/licenses/go.yaml.in-yaml-v3-LICENSE" "${module_cache}/go.yaml.in/yaml/v3@v3.0.5/LICENSE"
cmp "${repo}/third_party/licenses/go.yaml.in-yaml-v3-NOTICE" "${module_cache}/go.yaml.in/yaml/v3@v3.0.5/NOTICE"
cmp "${repo}/third_party/licenses/golang.org-x-crypto-LICENSE" "${module_cache}/golang.org/x/crypto@v0.52.0/LICENSE"
cmp "${repo}/third_party/licenses/golang.org-x-sys-LICENSE" "${module_cache}/golang.org/x/sys@v0.45.0/LICENSE"
cmp "${repo}/third_party/licenses/golang.org-x-term-LICENSE" "${module_cache}/golang.org/x/term@v0.43.0/LICENSE"
cmp "${repo}/third_party/licenses/golang.org-go-stdlib-LICENSE" "${go_license}"
printf 'verified seven reachable module versions, Go %s stdlib license, and exact upstream license/notice bytes\n' "${PIGLET_GO_VERSION}"
