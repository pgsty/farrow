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

for dependency in jq ssh ping ps stat; do
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
base_config_path=${workdir}/farrow-full.yaml
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
  printf '(cd %q && FARROW_HOME=%q %q' "${workdir}" "${data_root}" "${binary}" >>"${commands_log}"
  for argument in "$@"; do
    printf ' %q' "${argument}" >>"${commands_log}"
  done
  printf ')\n' >>"${commands_log}"
}

run_farrow() {
  local evidence_name=$1
  shift
  record_command "$@"
  (cd "${workdir}" && FARROW_HOME="${data_root}" "${binary}" "$@") \
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
      (cd "${workdir}" && FARROW_HOME="${data_root}" "${binary}" stop --json) \
        >"${evidence_root}/trap-stop.stdout" 2>"${evidence_root}/trap-stop.stderr" || stop_status=$?
      record_command destroy --force --json
      (cd "${workdir}" && FARROW_HOME="${data_root}" "${binary}" destroy --force --json) \
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

# Read and validate the already-installed host-global network. This script never
# calls network install or uninstall, even from its failure trap.
run_farrow network-before network status --json
assert_jq 'private network is installed, healthy, and lease-free' "${evidence_root}/network-before.stdout" \
  '.preflight.ready == true and .preflight.installation.healthy == true and (.preflight.installation.status == "exact" or .preflight.installation.status == "protected") and .lease.available == true and .lease.active == false'
network_cidr=$(jq -er '.preflight.cidr' "${evidence_root}/network-before.stdout")
[[ ${network_cidr} == */24 && ${network_cidr} == *.0/24 ]] || { printf 'installed network is not a canonical /24: %s\n' "${network_cidr}" >&2; exit 7; }
network_prefix=${network_cidr%.0/24}
address_host=${network_prefix}.1
address_dhcp_end=${network_prefix}.8
address_meta=${network_prefix}.10
address_node1=${network_prefix}.11
address_node2=${network_prefix}.12
address_node3=${network_prefix}.13

if [[ ${network_cidr} == 10.10.10.0/24 ]]; then
  record_command init full
  (cd "${workdir}" && FARROW_HOME="${data_root}" "${binary}" init full) \
    >"${base_config_path}" 2>"${evidence_root}/init-full.stderr"
else
  record_command init full --network-cidr "${network_cidr}"
  (cd "${workdir}" && FARROW_HOME="${data_root}" "${binary}" init full --network-cidr "${network_cidr}") \
    >"${base_config_path}" 2>"${evidence_root}/init-full.stderr"
fi

run_farrow validate-full validate -f "${base_config_path}" --json
if jq -e \
  --arg cidr "${network_cidr}" \
  --arg host_address "${address_host}" --arg dhcp_end "${address_dhcp_end}" \
  --arg a10 "${address_meta}" --arg a11 "${address_node1}" --arg a12 "${address_node2}" --arg a13 "${address_node3}" \
  '.valid == true and .resolved.name == "full" and .resolved.image == "u24" and .resolved.network == "private" and .resolved.private.cidr == $cidr and .resolved.private.host_address == $host_address and .resolved.private.dhcp_end == $dhcp_end and .resolved.ssh_user == "dba" and .resolved.ssh_wait_timeout_ns == 180000000000 and (.resolved.nodes | length) == 4 and [.resolved.nodes[].name] == ["meta","node-1","node-2","node-3"] and [.resolved.nodes[].address] == [$a10,$a11,$a12,$a13] and [.resolved.nodes[].control] == [true,false,false,false] and .resolved.nodes[0].host_aliases == ["i.pigsty","api.pigsty","cli.pigsty","sss.pigsty","adm.pigsty","lab.pigsty","wiki.pigsty","git.pigsty"] and ([.resolved.nodes[1:][] | select(((.host_aliases // []) | length) != 0)] | length) == 0 and ([.resolved.nodes[] | select(((.forwards // []) | length) != 0)] | length) == 0 and .resolved.nodes[0].cpus == 2 and .resolved.nodes[0].memory_bytes == 4294967296 and ([.resolved.nodes[1:][] | select(.cpus != 1 or .memory_bytes != 2147483648)] | length) == 0 and ([.resolved.nodes[] | select(.root_disk_bytes != 68719476736 or (.disks | length) != 1 or .disks[0].name != "data" or .disks[0].size_bytes != 137438953472 or .disks[0].mount != "/data" or (.disks[0].filesystem // "") != "" or .disks[0].persistent != false)] | length) == 0' \
  "${evidence_root}/validate-full.stdout" >/dev/null; then
  printf 'PASS\tembedded full profile resolves exact four-node topology and storage\n' >>"${assertions_log}"
else
  printf 'FAIL\tembedded full profile resolves exact four-node topology and storage\n' >>"${assertions_log}"
  exit 1
fi

private_share_from_host=farrow-full-readonly-share
printf '%s\n' "${private_share_from_host}" >"${host_share}/from-host"
jq -n \
  --arg cidr "${network_cidr}" --arg host_address "${address_host}" --arg dhcp_end "${address_dhcp_end}" \
  --arg a10 "${address_meta}" --arg a11 "${address_node1}" --arg a12 "${address_node2}" --arg a13 "${address_node3}" \
  --arg share_host "${host_share}" '
  def data_disk: {name: "data", size: "128GiB", mount: "/data", filesystem: "auto", persistent: false};
  {
    version: 1, name: "full", arch: "native",
    network: {mode: "private", cidr: $cidr, host_address: $host_address, dhcp_end: $dhcp_end},
    defaults: {image: "u24"}, ssh: {user: "dba"},
    nodes: [
      {name: "meta", control: true, host_aliases: ["i.pigsty","api.pigsty","cli.pigsty","sss.pigsty","adm.pigsty","lab.pigsty","wiki.pigsty","git.pigsty"], address: $a10, cpus: 2, memory: "4GiB", root_disk: "64GiB", disks: [data_disk], shares: [{host: $share_host, guest: "/workspace"}]},
      {name: "node-1", address: $a11, cpus: 1, memory: "2GiB", root_disk: "64GiB", disks: [data_disk]},
      {name: "node-2", address: $a12, cpus: 1, memory: "2GiB", root_disk: "64GiB", disks: [data_disk]},
      {name: "node-3", address: $a13, cpus: 1, memory: "2GiB", root_disk: "64GiB", disks: [data_disk]}
    ]
  }' >"${config_path}"
run_farrow validate-share validate -f "${config_path}" --json
if jq -e --arg host "${host_share}" \
  '.valid == true and .resolved.nodes[0].shares == [{host: $host, guest: "/workspace", readonly: true}] and ([.resolved.nodes[1:][] | select(((.shares // []) | length) != 0)] | length) == 0' \
  "${evidence_root}/validate-share.stdout" >/dev/null; then
  printf 'PASS\tfull config resolves one explicit default-read-only 9p share\n' >>"${assertions_log}"
else
  printf 'FAIL\tfull config resolves one explicit default-read-only 9p share\n' >>"${assertions_log}"
  exit 1
fi

run_farrow preflight network preflight -f "${config_path}" --json
assert_jq 'full configuration passes private network preflight' "${evidence_root}/preflight.stdout" \
  '.ready == true and .installation.healthy == true and (.installation.status == "exact" or .installation.status == "protected")'
run_farrow plan plan -f "${config_path}" --json
assert_jq 'full plan is a non-destructive four-node create' "${evidence_root}/plan.stdout" \
  '.action == "create" and .destructive == false and (.nodes | length) == 4 and ([.nodes[]] | sort) == ["meta","node-1","node-2","node-3"]'

run_farrow up up -f "${config_path}" --rollback --json
prove_owned_project || { printf 'public full up created an unprovable project marker\n' >&2; exit 7; }
[[ $(jq -er '.project_id' "${evidence_root}/up.stdout") == "${expected_project_id}" ]] || { printf 'full status project ID does not match the owned marker\n' >&2; exit 7; }
jq -n --arg project_id "${expected_project_id}" --arg project_root "${project_root}" --arg data_root "${data_root}" --arg network_cidr "${network_cidr}" \
  '{project_id:$project_id, project_root:$project_root, data_root:$data_root, network_cidr:$network_cidr, ownership_proved:true}' \
  >"${evidence_root}/ownership.json"
if jq -e \
  --arg a10 "${address_meta}" --arg a11 "${address_node1}" --arg a12 "${address_node2}" --arg a13 "${address_node3}" \
  '(.nodes | length) == 4 and ([.nodes[].name] | sort) == ["meta","node-1","node-2","node-3"] and ([.nodes[].address] | sort) == ([$a10,$a11,$a12,$a13] | sort) and ([.nodes[] | select(.state != "running" or .runtime != "running" or .pid <= 0)] | length) == 0' \
  "${evidence_root}/up.stdout" >/dev/null; then
  printf 'PASS\tpublic full up reached four running nodes on exact addresses\n' >>"${assertions_log}"
else
  printf 'FAIL\tpublic full up reached four running nodes on exact addresses\n' >>"${assertions_log}"
  exit 1
fi
run_farrow image-info image info --json u24
assert_jq 'the full project uses a cached checksum-bound u24 image' "${evidence_root}/image-info.stdout" \
  '.entry.alias == "u24" and (.entry.sha256 | test("^[0-9a-f]{64}$")) and .cached == true and (.metadata.virtual_size > 0)'

provision_script=${evidence_root}/provision-smoke.sh
cat >"${provision_script}" <<'PROVISION'
test "$(id -u)" -eq 0
install -d -o dba -g admin -m 0755 /var/lib/farrow-product-smoke
printf 'provisioned:%s\n' "$(hostname)" > /var/lib/farrow-product-smoke/provisioned
chown dba:admin /var/lib/farrow-product-smoke/provisioned
install -d -o dba -g admin -m 0755 /data/farrow-product-smoke
PROVISION
chmod 0600 "${provision_script}"
run_farrow provision provision --script "${provision_script}" --sudo --parallel 4 --json
assert_jq 'bounded parallel sudo provision completed on all four nodes' "${evidence_root}/provision.stdout" \
  '(.script.sha256 | length) == 64 and .successful == 4 and .failed == 0 and (.results | length) == 4 and ([.results[].node] | sort) == ["meta","node-1","node-2","node-3"] and ([.results[] | select(.success != true or .exit_code != 0)] | length) == 0'

{
  for address in "${address_meta}" "${address_node1}" "${address_node2}" "${address_node3}"; do
    printf 'ping %s\n' "${address}"
    if [[ ${host_os} == Darwin ]]; then
      ping -c 1 -W 2000 "${address}"
    else
      ping -c 1 -W 2 "${address}"
    fi
  done
} >"${evidence_root}/host-to-vm.log" 2>&1
printf 'PASS\thost reached all four private addresses\n' >>"${assertions_log}"

node_guest_script='\
expected_address=$1
canary=$2
expected_control=$3
share_canary=$4
test "$(id -un)" = dba
test "$(id -u)" -eq 88
test "$(cat /var/lib/farrow-product-smoke/provisioned)" = "provisioned:$(hostname)"
if test "${expected_control}" = true; then
  test -f /home/dba/.ssh/id_ed25519
  test ! -L /home/dba/.ssh/id_ed25519
  test "$(stat -c %u:%g:%a /home/dba/.ssh/id_ed25519)" = 88:88:600
  test "$(findmnt -n -o FSTYPE --target /workspace)" = 9p
  test "$(cat /workspace/from-host)" = "${share_canary}"
  ! touch /workspace/readonly-probe
else
  test ! -e /home/dba/.ssh/id_ed25519
  test ! -L /home/dba/.ssh/id_ed25519
fi
ip -4 -o addr show dev private0 | grep -F " ${expected_address}/24 " >/dev/null
defaults=$(ip -4 route show default)
test "$(printf "%s\n" "${defaults}" | awk "NF { count++ } END { print count+0 }")" -eq 1
! printf "%s\n" "${defaults}" | grep -Eq "(^|[[:space:]])dev[[:space:]]+private0([[:space:]]|$)"
if command -v resolvectl >/dev/null 2>&1; then
  private_dns=$(resolvectl dns private0 2>/dev/null || true)
  private_dns_values=${private_dns#*:}
  private_dns_values=$(printf "%s" "${private_dns_values}" | tr -d "[:space:]")
  test -z "${private_dns_values}" || test "${private_dns_values}" = "-"
elif command -v nmcli >/dev/null 2>&1; then
  test -z "$(nmcli -g IP4.DNS device show private0 2>/dev/null || true)"
else
  echo "guest has no per-link DNS inspector" >&2
  exit 91
fi
root_bytes=$(findmnt -b -n -o SIZE --target /)
data_bytes=$(findmnt -b -n -o SIZE --target /data)
test "${root_bytes}" -ge 64424509440
test "${data_bytes}" -ge 128849018880
mountpoint -q /data
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --connect-timeout 10 --max-time 30 https://example.com/ >/dev/null
elif command -v wget >/dev/null 2>&1; then
  wget -q --timeout=30 -O /dev/null https://example.com/
else
  echo "guest has neither curl nor wget" >&2
  exit 90
fi
printf "%s\n" "${canary}" > /data/farrow-product-smoke/canary
sync /data/farrow-product-smoke/canary
printf "address=%s user=%s uid=%s root_bytes=%s data_bytes=%s default=%s dns=%s canary=%s\n" "${expected_address}" "$(id -un)" "$(id -u)" "${root_bytes}" "${data_bytes}" "${defaults}" "${private_dns:-nmcli-empty}" "$(cat /data/farrow-product-smoke/canary)"
'

for node_address in \
  "meta:${address_meta}" \
  "node-1:${address_node1}" \
  "node-2:${address_node2}" \
  "node-3:${address_node3}"; do
  node=${node_address%%:*}
  address=${node_address#*:}
  canary=farrow-full-${expected_project_id}-${node}
  expected_control=false
  [[ ${node} == meta ]] && expected_control=true
  run_farrow "guest-${node}-initial" exec "${node}" -- sh -ceu "${node_guest_script}" sh "${address}" "${canary}" "${expected_control}" "${private_share_from_host}"
done
printf 'PASS\tall guests proved dba, control-only key and 9p share, root/data, internet, and private0 route/DNS contract\n' >>"${assertions_log}"

mesh_script='\
for peer in "$@"; do
  ip -4 route get "${peer}" | grep -Eq "(^|[[:space:]])dev[[:space:]]+private0([[:space:]]|$)"
  ping -c 1 -W 2 "${peer}" >/dev/null
done
'
run_farrow mesh-meta exec meta -- sh -ceu "${mesh_script}" sh "${address_node1}" "${address_node2}" "${address_node3}"
run_farrow mesh-node-1 exec node-1 -- sh -ceu "${mesh_script}" sh "${address_meta}" "${address_node2}" "${address_node3}"
run_farrow mesh-node-2 exec node-2 -- sh -ceu "${mesh_script}" sh "${address_meta}" "${address_node1}" "${address_node3}"
run_farrow mesh-node-3 exec node-3 -- sh -ceu "${mesh_script}" sh "${address_meta}" "${address_node1}" "${address_node2}"
printf 'PASS\tall twelve directed VM-to-VM paths used private0\n' >>"${assertions_log}"

control_script='\
for peer_name in node-1 node-2 node-3; do
  ssh -o BatchMode=yes -o ConnectTimeout=10 "${peer_name}" true
done
'
run_farrow control-lateral exec meta -- sh -ceu "${control_script}"
printf 'PASS\tcontrol reached workers over private0 and lateral SSH\n' >>"${assertions_log}"

prove_owned_project || { printf 'project ownership changed before stop\n' >&2; exit 7; }
run_farrow stop stop --json
assert_jq 'public stop reached four inactive stopped nodes' "${evidence_root}/stop.stdout" \
  '(.nodes | length) == 4 and ([.nodes[] | select(.state != "stopped" or .runtime != "inactive")] | length) == 0'
run_farrow status-stopped status --json
assert_jq 'status reports four stopped nodes' "${evidence_root}/status-stopped.stdout" \
  '(.nodes | length) == 4 and ([.nodes[] | select(.state != "stopped" or .runtime != "inactive")] | length) == 0'
run_farrow network-stopped network status --json
assert_jq 'full stop released the host-global lease without removing the network' "${evidence_root}/network-stopped.stdout" \
  '.lease.active == false and .preflight.ready == true and .preflight.installation.healthy == true'

run_farrow start start --json
assert_jq 'public start returned all four nodes to running' "${evidence_root}/start.stdout" \
  '(.nodes | length) == 4 and ([.nodes[] | select(.state != "running" or .runtime != "running" or .pid <= 0)] | length) == 0'
run_farrow status-running status --json
assert_jq 'status reports four restarted nodes' "${evidence_root}/status-running.stdout" \
  '(.nodes | length) == 4 and ([.nodes[] | select(.state != "running" or .runtime != "running" or .pid <= 0)] | length) == 0'

for node in meta node-1 node-2 node-3; do
  canary=farrow-full-${expected_project_id}-${node}
  if [[ ${node} == meta ]]; then
    run_farrow "guest-${node}-after-restart" exec "${node}" -- sh -ceu \
      'test "$(cat /data/farrow-product-smoke/canary)" = "$1"; test "$(cat /workspace/from-host)" = "$2"; printf "canary=%s share=%s\n" "$(cat /data/farrow-product-smoke/canary)" "$(cat /workspace/from-host)"' sh "${canary}" "${private_share_from_host}"
  else
    run_farrow "guest-${node}-after-restart" exec "${node}" -- sh -ceu \
      'test "$(cat /data/farrow-product-smoke/canary)" = "$1"; printf "canary=%s\n" "$(cat /data/farrow-product-smoke/canary)"' sh "${canary}"
  fi
  node_state=${project_root}/nodes/${node}/state.json
  [[ -f ${node_state} && ! -L ${node_state} && $(path_uid "${node_state}") == "${current_uid}" && $(path_mode "${node_state}") == 600 ]] || { printf 'private node state is not an owned mode-0600 regular file: %s\n' "${node}" >&2; exit 7; }
  jq -e --arg project_id "${expected_project_id}" --arg node "${node}" '.project_id == $project_id and .node == $node and .phase == "running" and (.image.digest | test("^[0-9a-f]{64}$"))' "${node_state}" >/dev/null
  cp "${node_state}" "${evidence_root}/node-state-${node}-before-destroy.json"
done
printf 'PASS\tall four data canaries and the meta 9p share survived stop/start\n' >>"${assertions_log}"

prove_owned_project || { printf 'project ownership changed before destroy\n' >&2; exit 7; }
run_farrow destroy destroy --force --json
cleanup_needed=0
assert_jq 'guarded full destroy returned four absent nodes' "${evidence_root}/destroy.stdout" \
  '(.nodes | length) == 4 and ([.nodes[] | select(.state != "absent" or .runtime != "inactive")] | length) == 0'
for node in meta node-1 node-2 node-3; do
  [[ ! -e ${project_root}/nodes/${node} && ! -L ${project_root}/nodes/${node} ]] || { printf 'private node artifacts remain after destroy: %s\n' "${node}" >&2; exit 1; }
done
if ps ax -o comm=,command= | awk -v root="${project_root}" 'index($1, "qemu-system") == 1 && index($0, root) { found=1 } END { exit found ? 0 : 1 }'; then
  printf 'project QEMU process remains after destroy\n' >&2
  exit 1
fi
run_farrow network-after network status --json
assert_jq 'destroy released the lease and preserved the installed network' "${evidence_root}/network-after.stdout" \
  '.lease.active == false and .preflight.ready == true and .preflight.installation.healthy == true'
printf 'PASS\tdestroy left no node artifacts or project QEMU\n' >>"${assertions_log}"

finished_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
{
  printf 'result=passed\n'
  printf 'started_at=%s\n' "${started_at}"
  printf 'finished_at=%s\n' "${finished_at}"
  printf 'project_id=%s\n' "${expected_project_id}"
  printf 'project_root=%s\n' "${project_root}"
  printf 'network_cidr=%s\n' "${network_cidr}"
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

printf 'passed public private full product smoke; evidence: %s\n' "${evidence_root}"
