#!/usr/bin/env bash
set -euo pipefail

source_root=${PIGSTY_SOURCE:-}
[[ ${source_root} == /* && -d ${source_root} && -f ${source_root}/Makefile ]] || {
  printf 'PIGSTY_SOURCE must be an absolute Pigsty source directory\n' >&2
  exit 2
}
source_root=$(cd "${source_root}" && pwd -P)
repo=$(cd "$(dirname "$0")/.." && pwd -P)
root=$(mktemp -d "${TMPDIR:-/tmp}/farrow-pigsty-source-test.XXXXXX")
cleanup() {
  case ${root} in
    "${TMPDIR:-/tmp}"/farrow-pigsty-source-test.*) rm -rf -- "${root}" ;;
    *) printf 'refuse unsafe Pigsty source test cleanup: %s\n' "${root}" >&2 ;;
  esac
}
trap cleanup EXIT

makefile=${source_root}/Makefile
section=${root}/sandbox-section
sed -n '/#                       5[.] Farrow/,/#                       6[.] Testing/p' "${makefile}" >"${section}"
rg -F 'PIGSTY_VM?=pigsty-vm' "${makefile}" >/dev/null
rg -F 'PIGSTY_INVENTORY?=$(CURDIR)/.farrow/pigsty.yml' "${makefile}" >/dev/null
rg -F 'PIGSTY_SSH_CONFIG?=$(CURDIR)/.farrow/ssh_config' "${makefile}" >/dev/null
rg -F '.farrow/' "${source_root}/.gitignore" >/dev/null
if rg -i '\bvagrant\b|virtualbox|libvirt|VM_PROVIDER|suspend|resume|nuke' "${section}" >/dev/null; then
  printf 'Pigsty sandbox section retains a predecessor runtime concept\n' >&2
  exit 1
fi
if rg -n '^(meta8|full8|simu8|vmeta|vfull|vsimu|vo|vp|vr|vd|va):' "${makefile}" >/dev/null; then
  printf 'Pigsty Makefile retains a retired VM target\n' >&2
  exit 1
fi
if rg -i 'copy.*vagrant|pigsty/vagrant' "${source_root}/bin/release" >/dev/null; then
  printf 'Pigsty source release still packages a predecessor runtime\n' >&2
  exit 1
fi
bash -n "${source_root}/bin/release"

dry=${root}/dry-run
make -C "${source_root}" -n sandbox \
  PIGSTY_VM="${repo}/bin/pigsty-vm" FARROW_BIN="${repo}/bin/farrow" \
  VM_SPEC=full VM_SCALE=2 VM_IMAGE=u24 VM_NETWORK_CIDR=172.31.251.0/24 \
  PIGSTY_INVENTORY="${root}/pigsty.yml" PIGSTY_SSH_CONFIG="${root}/ssh_config" >"${dry}"
rg -F 'VM_SPEC="full" VM_IMAGE="u24" VM_SCALE="2" VM_ARCH="native" VM_NETWORK_CIDR="172.31.251.0/24"' "${dry}" >/dev/null
rg -F ' inventory --output ' "${dry}" >/dev/null
rg -F ' preflight' "${dry}" >/dev/null
rg -F ' up' "${dry}" >/dev/null
rg -F ' ssh-config >' "${dry}" >/dev/null

inventory_line=$(rg -n ' inventory --output ' "${dry}" | head -1 | cut -d: -f1)
preflight_line=$(rg -n ' preflight' "${dry}" | head -1 | cut -d: -f1)
up_line=$(rg -n ' up$' "${dry}" | head -1 | cut -d: -f1)
ssh_line=$(rg -n ' ssh-config >' "${dry}" | head -1 | cut -d: -f1)
(( inventory_line < preflight_line && preflight_line < up_line && up_line < ssh_line )) || {
  printf 'Pigsty sandbox operations are not sequentially ordered\n' >&2
  exit 1
}

PIGSTY_SOURCE=${source_root} go test ./internal/pigsty -run TestPigstyInventoryCorpus -count=1
printf 'Pigsty source integration tests passed\n'
