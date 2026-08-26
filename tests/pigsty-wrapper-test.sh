#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)
wrapper=${repo}/packaging/pigsty/vm
root=$(mktemp -d "${TMPDIR:-/tmp}/farrow-wrapper-test.XXXXXX")
cleanup() {
  case ${root} in
    "${TMPDIR:-/tmp}"/farrow-wrapper-test.*) rm -rf -- "${root}" ;;
    *) printf 'refuse unsafe wrapper-test cleanup: %s\n' "${root}" >&2 ;;
  esac
}
trap cleanup EXIT
install -d -m 0700 "${root}/bin" "${root}/tmp" "${root}/cwd"

cat >"${root}/bin/farrow" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'farrow' >>"${FAKE_LOG}"
printf '\t%q' "$@" >>"${FAKE_LOG}"
printf '\n' >>"${FAKE_LOG}"
if [[ ${1:-} == init ]]; then
  cat <<'YAML'
version: 1
name: fixture
network: {mode: private}
nodes: [{name: meta, address: 10.10.10.10}]
YAML
fi
FAKE
chmod 0755 "${root}/bin/farrow"
export FAKE_LOG=${root}/calls.log

(
  cd "${root}/cwd"
  TMPDIR=${root}/tmp FARROW_BIN=${root}/bin/farrow VM_SPEC=full VM_SCALE=2 VM_IMAGE=d13 VM_NETWORK_CIDR=172.31.251.0/24 "${wrapper}" up --json
)
rg -F $'farrow\tinit\tfull\t--scale\t2\t--image\td13\t--network-cidr\t172.31.251.0/24' "${FAKE_LOG}" >/dev/null
rg -e $'farrow\tvalidate\t-f\t.*/farrow-pigsty-profile\.' "${FAKE_LOG}" >/dev/null
rg -e $'farrow\tup\t-f\t.*/farrow-pigsty-profile\.[^[:space:]]+\t--json' "${FAKE_LOG}" >/dev/null
[[ -z $(find "${root}/tmp" -mindepth 1 -maxdepth 1 -name 'farrow-pigsty-profile.*' -print -quit) ]]

: >"${FAKE_LOG}"
(
  cd "${root}/cwd"
  TMPDIR=${root}/tmp FARROW_BIN=${root}/bin/farrow VM_SPEC=minio VM_NETWORK_CIDR=172.31.251.0/24 "${wrapper}" preflight --json
)
rg -F $'farrow\tinit\tminio\t--scale\t1\t--network-cidr\t172.31.251.0/24' "${FAKE_LOG}" >/dev/null
rg -e $'farrow\tnetwork\tpreflight\t-f\t.*/farrow-pigsty-profile\.[^[:space:]]+\t--json' "${FAKE_LOG}" >/dev/null
[[ -z $(find "${root}/tmp" -mindepth 1 -maxdepth 1 -name 'farrow-pigsty-profile.*' -print -quit) ]]

: >"${FAKE_LOG}"
FARROW_BIN=${root}/bin/farrow VM_SPEC=rpm VM_IMAGE=u24 VM_FORCE_UNIFORM_IMAGE=1 "${wrapper}" init --json >/dev/null
rg -F -- '--force-uniform-image' "${FAKE_LOG}" >/dev/null

: >"${FAKE_LOG}"
FARROW_BIN=${root}/bin/farrow PIGSTY_ROOT=${root}/cwd VM_SPEC=full VM_NETWORK_CIDR=172.31.251.0/24 "${wrapper}" inventory --output "${root}/cwd/pigsty.yml" --force
rg -F $'farrow\tpigsty\tinventory\t--profile\tfull\t--root\t'"${root}/cwd"$'\t--scale\t1\t--network-cidr\t172.31.251.0/24\t--output\t'"${root}/cwd/pigsty.yml"$'\t--force' "${FAKE_LOG}" >/dev/null

: >"${FAKE_LOG}"
(
  cd "${root}/cwd"
  TMPDIR=${root}/tmp FARROW_BIN=${root}/bin/farrow VM_SPEC=full VM_IMAGE=u24 VM_NETWORK_CIDR=172.31.251.0/24 "${wrapper}" recreate --force
)
rg -F $'farrow\tinit\tfull\t--scale\t1\t--image\tu24\t--network-cidr\t172.31.251.0/24' "${FAKE_LOG}" >/dev/null
rg -e $'farrow\trecreate\t-f\t.*/farrow-pigsty-profile\.[^[:space:]]+\t--force' "${FAKE_LOG}" >/dev/null
[[ -z $(find "${root}/tmp" -mindepth 1 -maxdepth 1 -name 'farrow-pigsty-profile.*' -print -quit) ]]

: >"${FAKE_LOG}"
FARROW_BIN=${root}/bin/farrow "${wrapper}" destroy --force
rg -F $'farrow\tdestroy\t--force' "${FAKE_LOG}" >/dev/null
if rg -F -- '--force' "${FAKE_LOG}" | rg -v $'destroy\t--force' >/dev/null; then
  printf 'wrapper injected --force unexpectedly\n' >&2
  exit 1
fi

: >"${FAKE_LOG}"
FARROW_BIN=${root}/bin/farrow "${wrapper}" exec node-1 -- printf '%s' 'a b'
rg -F $'farrow\texec\tnode-1\t--\tprintf\t%s\ta\\ b' "${FAKE_LOG}" >/dev/null

: >"${FAKE_LOG}"
FARROW_BIN=${root}/bin/farrow "${wrapper}" ssh-config --install --name pigsty
FARROW_BIN=${root}/bin/farrow "${wrapper}" hosts install --yes
FARROW_BIN=${root}/bin/farrow "${wrapper}" network status --json
FARROW_BIN=${root}/bin/farrow "${wrapper}" repair --dry-run
rg -F $'farrow\tssh-config\t--install\t--name\tpigsty' "${FAKE_LOG}" >/dev/null
rg -F $'farrow\thosts\tinstall\t--yes' "${FAKE_LOG}" >/dev/null
rg -F $'farrow\tnetwork\tstatus\t--json' "${FAKE_LOG}" >/dev/null
rg -F $'farrow\trepair\t--dry-run' "${FAKE_LOG}" >/dev/null

if FARROW_BIN=${root}/bin/farrow VM_ARCH=definitely-foreign "${wrapper}" status >/dev/null 2>&1; then
  printf 'foreign VM_ARCH was accepted\n' >&2
  exit 1
fi
if FARROW_BIN=${root}/bin/farrow VM_NETWORK_CIDR='172.31.251.0/24 --force' "${wrapper}" init >/dev/null 2>&1; then
  printf 'unsafe VM_NETWORK_CIDR was accepted\n' >&2
  exit 1
fi
if FARROW_BIN=${root}/bin/farrow PIGSTY_ROOT=relative "${wrapper}" inventory >/dev/null 2>&1; then
  printf 'relative PIGSTY_ROOT was accepted\n' >&2
  exit 1
fi
printf 'Pigsty wrapper tests passed\n'
