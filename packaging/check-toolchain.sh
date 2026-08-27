#!/usr/bin/env bash
set -euo pipefail

mode=${1:-all}
case ${mode} in go|packages|goreleaser|signing|quality|all) ;; *) printf 'usage: %s go|packages|goreleaser|signing|quality|all\n' "$0" >&2; exit 2 ;; esac
root=$(cd "$(dirname "$0")" && pwd -P)
# shellcheck disable=SC1091
source "${root}/toolchain.env"

require_version() {
  local tool=$1 actual=$2 expected=$3
  [[ ${actual} == "${expected}" ]] || { printf '%s version %s does not match pinned %s\n' "${tool}" "${actual}" "${expected}" >&2; exit 3; }
  printf '%s %s\n' "${tool}" "${actual}"
}

check_go() {
  command -v go >/dev/null || { printf 'go is missing\n' >&2; exit 3; }
  require_version go "$(go env GOVERSION | sed 's/^go//')" "${FARROW_GO_VERSION}"
}
check_goreleaser() {
  command -v goreleaser >/dev/null || { printf 'goreleaser is missing\n' >&2; exit 3; }
  command -v jq >/dev/null || { printf 'jq is missing\n' >&2; exit 3; }
  local binary embedded_nfpm
  binary=$(command -v goreleaser)
  require_version goreleaser "$(goreleaser --version | awk '/GitVersion:/ {gsub(/^v/, "", $2); print $2; exit}')" "${FARROW_GORELEASER_VERSION}"
  embedded_nfpm=$(go version -m "${binary}" | awk '$1 == "dep" && $2 == "github.com/goreleaser/nfpm/v2" {sub(/^v/, "", $3); print $3; exit}')
  require_version 'goreleaser embedded nFPM' "${embedded_nfpm}" "${FARROW_GORELEASER_NFPM_VERSION}"
  printf 'jq %s\n' "$(jq --version | sed 's/^jq-//')"
}
check_nfpm() {
  command -v nfpm >/dev/null || { printf 'nfpm is missing\n' >&2; exit 3; }
  require_version nfpm "$(nfpm --version | awk '/GitVersion:/ {gsub(/^v/, "", $2); print $2; exit}')" "${FARROW_NFPM_VERSION}"
}
check_syft() {
  command -v syft >/dev/null || { printf 'syft is missing\n' >&2; exit 3; }
  require_version syft "$(syft version | awk '/^Version:/ {gsub(/^v/, "", $2); print $2; exit}')" "${FARROW_SYFT_VERSION}"
}
check_cosign() {
  command -v cosign >/dev/null || { printf 'cosign is missing\n' >&2; exit 3; }
  require_version cosign "$(cosign version | awk '/^GitVersion:/ {gsub(/^v/, "", $2); print $2; exit}')" "${FARROW_COSIGN_VERSION}"
}
check_staticcheck() {
  command -v staticcheck >/dev/null || { printf 'staticcheck is missing\n' >&2; exit 3; }
  require_version staticcheck "$(staticcheck -version | awk '{print $2; exit}')" "${FARROW_STATICCHECK_VERSION}"
}
check_govulncheck() {
  command -v govulncheck >/dev/null || { printf 'govulncheck is missing\n' >&2; exit 3; }
  require_version govulncheck "$(govulncheck -version | awk -F'@v' '/^Scanner:/ {print $2; exit}')" "${FARROW_GOVULNCHECK_VERSION}"
}

case ${mode} in
  go) check_go ;;
  packages) check_go; check_nfpm; check_syft ;;
  goreleaser) check_go; check_goreleaser; check_syft ;;
  signing) check_cosign ;;
  quality) check_go; check_staticcheck; check_govulncheck ;;
  all) check_go; check_goreleaser; check_nfpm; check_syft; check_cosign; check_staticcheck; check_govulncheck ;;
esac
