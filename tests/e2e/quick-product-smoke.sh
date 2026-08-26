#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# -ne 3 ]]; then
  printf 'usage: %s <absolute-farrow-binary> <absolute-existing-data-root> <absolute-new-evidence-root>\n' "$0" >&2
  exit 2
fi

binary_input=$1
data_root_input=$2
evidence_root_input=$3

for path in "${binary_input}" "${data_root_input}" "${evidence_root_input}"; do
  [[ ${path} == /* ]] || { printf 'all paths must be absolute: %s\n' "${path}" >&2; exit 2; }
done

for dependency in jq ssh ps stat; do
  command -v "${dependency}" >/dev/null 2>&1 || { printf 'required command is missing: %s\n' "${dependency}" >&2; exit 3; }
done
if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
  printf 'required SHA-256 command is missing: shasum or sha256sum\n' >&2
  exit 3
fi

binary_parent=$(cd "$(dirname "${binary_input}")" && pwd -P)
binary=${binary_parent}/$(basename "${binary_input}")
[[ -f ${binary} && -x ${binary} && ! -L ${binary} ]] || { printf 'farrow binary must be an executable regular non-symlink file: %s\n' "${binary}" >&2; exit 2; }
if command -v shasum >/dev/null 2>&1; then
  binary_sha256=$(shasum -a 256 "${binary}" | awk '{print $1}')
else
  binary_sha256=$(sha256sum "${binary}" | awk '{print $1}')
fi

[[ -d ${data_root_input} && ! -L ${data_root_input} ]] || { printf 'data root must be an existing real directory: %s\n' "${data_root_input}" >&2; exit 2; }
data_root=$(cd "${data_root_input}" && pwd -P)
current_uid=$(id -u)
host_os=$(uname -s)

case ${host_os} in
  Darwin|Linux) ;;
  *) printf 'product smoke supports Darwin and Linux hosts, got: %s\n' "${host_os}" >&2; exit 3 ;;
esac

path_uid() {
  if [[ ${host_os} == Darwin ]]; then
    stat -f '%u' "$1"
  else
    stat -c '%u' "$1"
  fi
}

path_mode() {
  if [[ ${host_os} == Darwin ]]; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

if [[ $(path_uid "${data_root}") != "${current_uid}" ]]; then
  printf 'data root is not owned by the invoking user: %s\n' "${data_root}" >&2
  exit 7
fi
case $(path_mode "${data_root}") in
  700) ;;
  *) printf 'data root must already be mode 0700: %s\n' "${data_root}" >&2; exit 7 ;;
esac

evidence_parent=$(cd "$(dirname "${evidence_root_input}")" && pwd -P)
evidence_name=$(basename "${evidence_root_input}")
[[ ${evidence_name} != . && ${evidence_name} != .. ]] || { printf 'unsafe evidence root name\n' >&2; exit 2; }
evidence_root=${evidence_parent}/${evidence_name}
if [[ -e ${evidence_root} || -L ${evidence_root} ]]; then
  printf 'refuse existing evidence root: %s\n' "${evidence_root}" >&2
  exit 2
fi
mkdir -m 0700 "${evidence_root}"
[[ -d ${evidence_root} && ! -L ${evidence_root} && $(path_uid "${evidence_root}") == "${current_uid}" && $(path_mode "${evidence_root}") == 700 ]] || {
  printf 'new evidence root is not an owned mode-0700 real directory: %s\n' "${evidence_root}" >&2
  exit 7
}
workdir=${evidence_root}/workdir
mkdir -m 0700 "${workdir}"
[[ -d ${workdir} && ! -L ${workdir} && $(path_uid "${workdir}") == "${current_uid}" && $(path_mode "${workdir}") == 700 ]] || {
  printf 'workdir is not an owned mode-0700 real directory: %s\n' "${workdir}" >&2
  exit 7
}
config_path=${workdir}/farrow.yaml
host_share=${workdir}/host-share
mkdir -m 0700 "${host_share}"
[[ -d ${host_share} && ! -L ${host_share} && $(path_uid "${host_share}") == "${current_uid}" && $(path_mode "${host_share}") == 700 ]] || {
  printf 'host share is not an owned mode-0700 real directory: %s\n' "${host_share}" >&2
  exit 7
}

commands_log=${evidence_root}/commands.log
assertions_log=${evidence_root}/assertions.log
started_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
expected_project_id=
project_root=
cleanup_needed=1

record_command() {
  local argument
  printf '(cd %q && FARROW_DATA_HOME=%q %q' "${workdir}" "${data_root}" "${binary}" >>"${commands_log}"
  for argument in "$@"; do
    printf ' %q' "${argument}" >>"${commands_log}"
  done
  printf ')\n' >>"${commands_log}"
}

run_farrow() {
  local evidence_name=$1
  shift
  record_command "$@"
  (cd "${workdir}" && FARROW_DATA_HOME="${data_root}" "${binary}" "$@") \
    >"${evidence_root}/${evidence_name}.stdout" \
    2>"${evidence_root}/${evidence_name}.stderr"
}

assert_jq() {
  local label=$1
  local file=$2
  local filter=$3
  if jq -e "${filter}" "${file}" >/dev/null; then
    printf 'PASS\t%s\n' "${label}" >>"${assertions_log}"
  else
    printf 'FAIL\t%s\n' "${label}" >>"${assertions_log}"
    return 1
  fi
}

prove_owned_project() {
  local marker=${workdir}/.farrow/project.json
  local marker_dir=${workdir}/.farrow
  local projects_dir=${data_root}/projects
  local root_marker candidate_id candidate_root canonical_root
  [[ -d ${marker_dir} && ! -L ${marker_dir} ]] || return 1
  [[ $(path_uid "${marker_dir}") == "${current_uid}" && $(path_mode "${marker_dir}") == 700 ]] || return 1
  [[ -f ${marker} && ! -L ${marker} ]] || return 1
  [[ $(path_uid "${marker}") == "${current_uid}" && $(path_mode "${marker}") == 600 ]] || return 1
  jq -e --arg root "${data_root}" \
    '.schema == 1 and .data_root == $root and (.project_id | test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"))' \
    "${marker}" >/dev/null || return 1
  candidate_id=$(jq -er '.project_id' "${marker}") || return 1
  if [[ -n ${expected_project_id} && ${candidate_id} != "${expected_project_id}" ]]; then
    return 1
  fi
  candidate_root=${data_root}/projects/${candidate_id}
  [[ -d ${projects_dir} && ! -L ${projects_dir} ]] || return 1
  [[ $(path_uid "${projects_dir}") == "${current_uid}" && $(path_mode "${projects_dir}") == 700 ]] || return 1
  [[ -d ${candidate_root} && ! -L ${candidate_root} ]] || return 1
  [[ $(path_uid "${candidate_root}") == "${current_uid}" && $(path_mode "${candidate_root}") == 700 ]] || return 1
  canonical_root=$(cd "${candidate_root}" && pwd -P) || return 1
  [[ ${canonical_root} == "${candidate_root}" ]] || return 1
  root_marker=${candidate_root}/project.json
  [[ -f ${root_marker} && ! -L ${root_marker} ]] || return 1
  [[ $(path_uid "${root_marker}") == "${current_uid}" && $(path_mode "${root_marker}") == 600 ]] || return 1
  cmp -s "${marker}" "${root_marker}" || return 1
  expected_project_id=${candidate_id}
  project_root=${candidate_root}
  return 0
}

cleanup() {
  local result=$?
  local stop_status=0 destroy_status=0
  trap - EXIT INT TERM HUP
  set +e
  if (( cleanup_needed )); then
    if prove_owned_project; then
      printf 'cleanup: marker ownership proved for project %s\n' "${expected_project_id}" >&2
      record_command stop --json
      (cd "${workdir}" && FARROW_DATA_HOME="${data_root}" "${binary}" stop --json) \
        >"${evidence_root}/trap-stop.stdout" 2>"${evidence_root}/trap-stop.stderr" || stop_status=$?
      record_command destroy --force --json
      (cd "${workdir}" && FARROW_DATA_HOME="${data_root}" "${binary}" destroy --force --json) \
        >"${evidence_root}/trap-destroy.stdout" 2>"${evidence_root}/trap-destroy.stderr" || destroy_status=$?
      printf 'stop_exit=%d\ndestroy_exit=%d\n' "${stop_status}" "${destroy_status}" >"${evidence_root}/trap-cleanup-status.txt"
      if (( stop_status != 0 || destroy_status != 0 )); then
        printf 'cleanup incomplete: stop=%d destroy=%d; evidence retained at %s\n' "${stop_status}" "${destroy_status}" "${evidence_root}" >&2
      fi
    elif [[ -e ${workdir}/.farrow || -L ${workdir}/.farrow ]]; then
      printf 'cleanup refused: project marker ownership could not be proved; evidence retained at %s\n' "${evidence_root}" >&2
    fi
  fi
  exit "${result}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

{
  printf 'started_at=%s\n' "${started_at}"
  printf 'binary=%s\n' "${binary}"
  printf 'binary_sha256=%s\n' "${binary_sha256}"
  printf 'data_root=%s\n' "${data_root}"
  printf 'workdir=%s\n' "${workdir}"
  printf 'host='; uname -a
  printf 'uid=%s\n' "${current_uid}"
} >"${evidence_root}/run.env"
"${binary}" version >"${evidence_root}/farrow-version.txt" 2>&1

run_farrow validate-quick validate --json
assert_jq 'zero-config resolves the Quick product contract' "${evidence_root}/validate-quick.stdout" \
  '.valid == true and .source == "builtin:quick" and .resolved.name == "quick" and .resolved.image == "u24" and .resolved.network == "user" and .resolved.ssh_user == "dba" and .resolved.ssh_wait_timeout_ns == 180000000000 and (.resolved.nodes | length) == 1 and .resolved.nodes[0].name == "meta" and .resolved.nodes[0].control == true and .resolved.nodes[0].cpus == 2 and .resolved.nodes[0].memory_bytes == 4294967296 and .resolved.nodes[0].root_disk_bytes == 68719476736 and (.resolved.nodes[0].disks | length) == 1 and .resolved.nodes[0].disks[0].name == "data" and .resolved.nodes[0].disks[0].size_bytes == 68719476736 and .resolved.nodes[0].disks[0].mount == "/data" and (.resolved.nodes[0].disks[0].filesystem // "") == "" and .resolved.nodes[0].disks[0].persistent == false and ([.resolved.nodes[0].forwards[] | [.bind,.host,.guest,.protocol]] | sort) == ([ ["127.0.0.1",15432,5432,"tcp"], ["127.0.0.1",13000,3000,"tcp"], ["127.0.0.1",18080,80,"tcp"], ["127.0.0.1",18443,443,"tcp"] ] | sort)'

quick_share_from_host=farrow-share-from-host
printf '%s\n' "${quick_share_from_host}" >"${host_share}/from-host"
jq -n --arg host "${host_share}" '{
  version: 1,
  name: "quick",
  network: {mode: "user"},
  defaults: {image: "u24", cpus: 2, memory: "4GiB", root_disk: "64GiB"},
  ssh: {user: "dba", wait_timeout: "180s"},
  nodes: [{
    name: "meta", control: true,
    disks: [{name: "data", size: "64GiB", mount: "/data", persistent: false}],
    shares: [{host: $host, guest: "/workspace", readonly: false}],
    forwards: [
      {bind: "127.0.0.1", host: 15432, guest: 5432, protocol: "tcp"},
      {bind: "127.0.0.1", host: 13000, guest: 3000, protocol: "tcp"},
      {bind: "127.0.0.1", host: 18080, guest: 80, protocol: "tcp"},
      {bind: "127.0.0.1", host: 18443, guest: 443, protocol: "tcp"}
    ]
  }]
}' >"${config_path}"
run_farrow validate-share validate -f "${config_path}" --json
if jq -e --arg host "${host_share}" \
  '.valid == true and .resolved.nodes[0].shares == [{host: $host, guest: "/workspace", readonly: false}]' \
  "${evidence_root}/validate-share.stdout" >/dev/null; then
  printf 'PASS\tQuick config resolves one explicit read-write 9p share\n' >>"${assertions_log}"
else
  printf 'FAIL\tQuick config resolves one explicit read-write 9p share\n' >>"${assertions_log}"
  exit 1
fi

run_farrow up up -f "${config_path}" --json
prove_owned_project || { printf 'public up created an unprovable project marker\n' >&2; exit 7; }
[[ $(jq -er '.project_id' "${evidence_root}/up.stdout") == "${expected_project_id}" ]] || { printf 'Quick status project ID does not match the owned marker\n' >&2; exit 7; }
jq -n --arg project_id "${expected_project_id}" --arg project_root "${project_root}" --arg data_root "${data_root}" \
  '{project_id:$project_id, project_root:$project_root, data_root:$data_root, ownership_proved:true}' \
  >"${evidence_root}/ownership.json"
assert_jq 'public up reached a running dba Quick VM with loopback forwards' "${evidence_root}/up.stdout" \
  '.ssh_port as $ssh_port | .project_id != "" and .node == "meta" and .state == "running" and .ssh_user == "dba" and .ssh_host == "127.0.0.1" and $ssh_port > 0 and (.forwards | length) == 5 and ([.forwards[].host] | unique | length) == 5 and ([.forwards[] | select(.bind != "127.0.0.1")] | length) == 0 and ([.forwards[] | select(.guest == 22 and .host == $ssh_port)] | length) == 1 and ([.forwards[] | select(.guest != 22) | .guest] | sort) == [80,443,3000,5432]'

quick_canary=farrow-quick-${expected_project_id}
provision_script=${evidence_root}/provision-smoke.sh
cat >"${provision_script}" <<'PROVISION'
test "$(id -u)" -eq 0
install -d -o dba -g admin -m 0755 /var/lib/farrow-product-smoke
printf 'provisioned:%s\n' "$(hostname)" > /var/lib/farrow-product-smoke/provisioned
chown dba:admin /var/lib/farrow-product-smoke/provisioned
install -d -o dba -g admin -m 0755 /data/farrow-product-smoke
PROVISION
chmod 0600 "${provision_script}"
run_farrow provision provision --script "${provision_script}" --sudo --json
assert_jq 'bounded sudo provision completed on the Quick node' "${evidence_root}/provision.stdout" \
  '(.script.sha256 | length) == 64 and .successful == 1 and .failed == 0 and (.results | length) == 1 and .results[0].node == "meta" and .results[0].success == true and .results[0].exit_code == 0'

quick_guest_script='\
test "$(id -un)" = dba
test "$(id -u)" -eq 88
test "$(cat /var/lib/farrow-product-smoke/provisioned)" = "provisioned:$(hostname)"
test "$(findmnt -n -o FSTYPE --target /workspace)" = 9p
test "$(cat /workspace/from-host)" = "$2"
printf "%s\n" "$3" > /workspace/from-guest
root_bytes=$(findmnt -b -n -o SIZE --target /)
data_bytes=$(findmnt -b -n -o SIZE --target /data)
test "${root_bytes}" -ge 64424509440
test "${data_bytes}" -ge 64424509440
mountpoint -q /data
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --connect-timeout 10 --max-time 30 https://example.com/ >/dev/null
elif command -v wget >/dev/null 2>&1; then
  wget -q --timeout=30 -O /dev/null https://example.com/
else
  echo "guest has neither curl nor wget" >&2
  exit 90
fi
printf "%s\n" "$1" > /data/farrow-product-smoke/canary
sync /data/farrow-product-smoke/canary
printf "user=%s uid=%s root_bytes=%s data_bytes=%s canary=%s\n" "$(id -un)" "$(id -u)" "${root_bytes}" "${data_bytes}" "$(cat /data/farrow-product-smoke/canary)"
'
quick_share_from_guest=farrow-share-from-guest
run_farrow guest-initial exec -- sh -ceu "${quick_guest_script}" sh "${quick_canary}" "${quick_share_from_host}" "${quick_share_from_guest}"
[[ $(cat "${host_share}/from-guest") == "${quick_share_from_guest}" ]] || { printf 'guest write did not reach the Quick host share\n' >&2; exit 1; }
printf 'PASS\tQuick 9p share completed host-to-guest and guest-to-host writes\n' >>"${assertions_log}"
run_farrow image-info image info --json u24
assert_jq 'the running Quick VM uses a cached checksum-bound u24 image' "${evidence_root}/image-info.stdout" \
  '.entry.alias == "u24" and (.entry.sha256 | test("^[0-9a-f]{64}$")) and .cached == true and (.metadata.virtual_size > 0)'

prove_owned_project || { printf 'project ownership changed before stop\n' >&2; exit 7; }
run_farrow stop stop --json
assert_jq 'public stop reached stopped state' "${evidence_root}/stop.stdout" '.node == "meta" and .state == "stopped"'
run_farrow status-stopped status --json
assert_jq 'status reports the stopped VM' "${evidence_root}/status-stopped.stdout" '.node == "meta" and .state == "stopped"'

run_farrow start start --json
assert_jq 'public start returned the VM to running' "${evidence_root}/start.stdout" '.node == "meta" and .state == "running"'
run_farrow status-running status --json
assert_jq 'status reports the restarted VM' "${evidence_root}/status-running.stdout" '.node == "meta" and .state == "running"'
quick_share_after_restart=farrow-share-after-restart
printf '%s\n' "${quick_share_after_restart}" >"${host_share}/from-host"
run_farrow guest-after-restart exec -- sh -ceu 'test "$(cat /data/farrow-product-smoke/canary)" = "$1"; test "$(cat /workspace/from-host)" = "$2"; test "$(cat /workspace/from-guest)" = "$3"; printf "canary=%s share=%s\n" "$(cat /data/farrow-product-smoke/canary)" "$(cat /workspace/from-host)"' sh "${quick_canary}" "${quick_share_after_restart}" "${quick_share_from_guest}"
printf 'PASS\tQuick 9p share survived stop/start and reflected a later host write\n' >>"${assertions_log}"

node_state=${project_root}/nodes/meta/state.json
[[ -f ${node_state} && ! -L ${node_state} && $(path_uid "${node_state}") == "${current_uid}" && $(path_mode "${node_state}") == 600 ]] || { printf 'Quick node state is not an owned mode-0600 regular file\n' >&2; exit 7; }
jq -e --arg project_id "${expected_project_id}" '.project_id == $project_id and .node == "meta" and .phase == "running" and (.image.digest | test("^[0-9a-f]{64}$"))' "${node_state}" >/dev/null
cp "${node_state}" "${evidence_root}/node-state-before-destroy.json"

prove_owned_project || { printf 'project ownership changed before destroy\n' >&2; exit 7; }
run_farrow destroy destroy --force --json
cleanup_needed=0
assert_jq 'guarded destroy returned absent' "${evidence_root}/destroy.stdout" '.node == "meta" and .state == "absent"'
[[ ! -e ${project_root}/nodes/meta && ! -L ${project_root}/nodes/meta ]] || { printf 'Quick node artifacts remain after destroy\n' >&2; exit 1; }
if ps ax -o comm=,command= | awk -v root="${project_root}" 'index($1, "qemu-system") == 1 && index($0, root) { found=1 } END { exit found ? 0 : 1 }'; then
  printf 'project QEMU process remains after destroy\n' >&2
  exit 1
fi
printf 'PASS\tdestroy left no node artifacts or project QEMU\n' >>"${assertions_log}"

finished_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
{
  printf 'result=passed\n'
  printf 'started_at=%s\n' "${started_at}"
  printf 'finished_at=%s\n' "${finished_at}"
  printf 'project_id=%s\n' "${expected_project_id}"
  printf 'project_root=%s\n' "${project_root}"
  printf 'binary_sha256=%s\n' "${binary_sha256}"
} >"${evidence_root}/result.txt"

sha256_path() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

(
  cd "${evidence_root}"
  while IFS= read -r path; do
    printf '%s  %s\n' "$(sha256_path "${path}")" "${path#./}"
  done < <(find . -type f ! -name SHA256SUMS -print | LC_ALL=C sort)
) >"${evidence_root}/SHA256SUMS"

printf 'passed public Quick product smoke; evidence: %s\n' "${evidence_root}"
