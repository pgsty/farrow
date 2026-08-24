#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 4 || $# -gt 5 ]]; then
  printf 'usage: %s <piglet-binary> <project-workdir> <data-root> <new-evidence-root> [cycles]\n' "$0" >&2
  exit 2
fi

binary=$1
workdir=$2
data_root=$3
evidence_root=$4
cycles=${5:-30}

for path in "${binary}" "${workdir}" "${data_root}" "${evidence_root}"; do
  [[ ${path} == /* ]] || { printf 'all paths must be absolute: %s\n' "${path}" >&2; exit 2; }
done
[[ -x ${binary} ]] || { printf 'piglet binary is not executable: %s\n' "${binary}" >&2; exit 2; }
[[ -d ${workdir} && -d ${data_root} ]] || { printf 'workdir/data root must exist\n' >&2; exit 2; }
if [[ ! ${cycles} =~ ^[0-9]+$ ]] || (( cycles < 1 || cycles > 30 )); then
  printf 'cycles must be 1..30\n' >&2
  exit 2
fi
[[ ! -e ${evidence_root} ]] || { printf 'refuse existing evidence root: %s\n' "${evidence_root}" >&2; exit 1; }

install -d -m 0700 "${evidence_root}"
project_id=$(jq -er '.project_id' "${workdir}/.piglet/project.json")
project_root=$(jq -er '.data_root' "${workdir}/.piglet/project.json")/projects/${project_id}
[[ -d ${project_root} ]] || { printf 'project root is missing: %s\n' "${project_root}" >&2; exit 1; }

printf 'cycle\tstop_seconds\tstart_seconds\tnodes\tresult\n' >"${evidence_root}/cycles.tsv"
for ((cycle = 1; cycle <= cycles; cycle++)); do
  cycle_name=$(printf 'cycle-%02d' "${cycle}")
  cycle_root="${evidence_root}/${cycle_name}"
  install -d -m 0700 "${cycle_root}"

  started=$(date +%s)
  (cd "${workdir}" && PIGLET_DATA_HOME="${data_root}" "${binary}" stop --json) >"${cycle_root}/stop.json" 2>"${cycle_root}/stop.stderr"
  stopped=$(date +%s)
  jq -e '.nodes | length == 4 and all(.state == "stopped" and .runtime == "inactive")' "${cycle_root}/stop.json" >/dev/null
  if ps ax -o comm=,command= | awk -v root="${project_root}" 'index($1, "qemu-system") == 1 && index($0, root) { found=1 } END { exit found ? 0 : 1 }'; then
    printf '%s left a project QEMU process after stop\n' "${cycle_name}" >&2
    exit 1
  fi
  (cd "${workdir}" && PIGLET_DATA_HOME="${data_root}" "${binary}" network status --json) >"${cycle_root}/network-stopped.json" 2>"${cycle_root}/network-stopped.stderr" || true
  jq -e '.lease.active == false' "${cycle_root}/network-stopped.json" >/dev/null

  (cd "${workdir}" && PIGLET_DATA_HOME="${data_root}" "${binary}" start --json) >"${cycle_root}/start.json" 2>"${cycle_root}/start.stderr"
  ready=$(date +%s)
  jq -e '.nodes | length == 4 and all(.state == "running" and .runtime == "running" and .pid > 0)' "${cycle_root}/start.json" >/dev/null
  for node in meta node-1 node-2 node-3; do
    (cd "${workdir}" && PIGLET_DATA_HOME="${data_root}" "${binary}" exec "${node}" -- test -f /data/piglet-full-canary)
  done
  for address in 10.10.10.10 10.10.10.11 10.10.10.12 10.10.10.13; do
    if [[ $(uname -s) == Darwin ]]; then
      ping -c 1 -W 1000 "${address}" >/dev/null
    else
      ping -c 1 -W 1 "${address}" >/dev/null
    fi
  done
  printf '%d\t%d\t%d\t4\tpassed\n' "${cycle}" "$((stopped - started))" "$((ready - stopped))" >>"${evidence_root}/cycles.tsv"
  printf 'passed %s stop=%ss start=%ss\n' "${cycle_name}" "$((stopped - started))" "$((ready - stopped))"
done

(cd "${evidence_root}" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256) >"${evidence_root}/SHA256SUMS"
printf 'passed %d private full stop/start cycles for project %s\n' "${cycles}" "${project_id}"
