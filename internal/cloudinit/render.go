// Package cloudinit renders the validated NoCloud seed payload and ISO.
package cloudinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

type Host struct {
	Name    string
	Address string
}

type Disk struct {
	Serial     string
	Mount      string
	Filesystem string
}

// Share is one QEMU virtio-9p export mounted inside the guest. Tag is a
// Farrow-generated stable identity, never a user-selected mount option.
type Share struct {
	Tag      string
	Guest    string
	Readonly bool
}

type PrivateNetwork struct {
	MAC         string
	Address     string
	Prefix      int
	HostAddress string
}

type Input struct {
	Node       string
	Hostname   string
	Generation uint64
	SpecHash   string
	SSHUser    string
	PublicKey  string
	PrivateKey string
	Control    bool
	MgmtMAC    string
	Private    *PrivateNetwork
	Hosts      []Host
	Disks      []Disk
	Shares     []Share
}

// Files are the only three entries allowed at the seed root.
type Files struct {
	MetaData      []byte
	UserData      []byte
	NetworkConfig []byte
}

var (
	dnsLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	dnsNamePattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
	serialPattern   = regexp.MustCompile(`^[a-z2-7]{20}$`)
	shareTagPattern = regexp.MustCompile(`^farrow-[0-9a-f]{20}$`)
	hashPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	userPattern     = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	mountPattern    = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
)

func mountPathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func reservedMount(path string) bool {
	for _, root := range []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/farrow"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func safeMount(path string) bool {
	return mountPattern.MatchString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && !reservedMount(path)
}

func safeShareMount(path, user string) bool {
	if !mountPattern.MatchString(path) || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return false
	}
	for _, reserved := range []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/farrow", filepath.Join("/home", user, ".ssh")} {
		if mountPathsOverlap(path, reserved) {
			return false
		}
	}
	return true
}

func singleLine(label, value string) error {
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must be a non-empty single line", label)
	}
	return nil
}

func validateInput(input Input) error {
	if !dnsLabelPattern.MatchString(input.Node) || !dnsLabelPattern.MatchString(input.Hostname) {
		return errors.New("node and hostname must be DNS labels")
	}
	if input.Generation == 0 {
		return errors.New("cloud-init generation must be positive")
	}
	if !hashPattern.MatchString(input.SpecHash) {
		return errors.New("spec hash must be 64 lowercase hexadecimal characters")
	}
	if !userPattern.MatchString(input.SSHUser) {
		return fmt.Errorf("invalid SSH user %q", input.SSHUser)
	}
	if err := singleLine("SSH public key", input.PublicKey); err != nil {
		return err
	}
	if !strings.HasPrefix(input.PublicKey, "ssh-ed25519 ") {
		return errors.New("M0 requires an Ed25519 SSH public key")
	}
	if _, err := net.ParseMAC(input.MgmtMAC); err != nil {
		return fmt.Errorf("invalid management MAC: %w", err)
	}
	if input.Private != nil {
		if _, err := net.ParseMAC(input.Private.MAC); err != nil {
			return fmt.Errorf("invalid private MAC: %w", err)
		}
		ip := net.ParseIP(input.Private.Address)
		hostIP := net.ParseIP(input.Private.HostAddress)
		if ip == nil || hostIP == nil || input.Private.Prefix < 1 || input.Private.Prefix > 32 {
			return errors.New("invalid private IPv4 address/prefix")
		}
		if ip.To4() == nil || hostIP.To4() == nil || ip.Equal(hostIP) {
			return errors.New("private network must be IPv4")
		}
		mask := net.CIDRMask(input.Private.Prefix, 32)
		if !ip.Mask(mask).Equal(hostIP.Mask(mask)) {
			return errors.New("private guest and host addresses are not in the same subnet")
		}
	}
	seenHost := make(map[string]struct{})
	for _, host := range input.Hosts {
		if !dnsNamePattern.MatchString(host.Name) || strings.Contains(host.Name, "..") || net.ParseIP(host.Address) == nil {
			return fmt.Errorf("invalid host mapping %q=%q", host.Name, host.Address)
		}
		if _, exists := seenHost[host.Name]; exists {
			return fmt.Errorf("duplicate host name %q", host.Name)
		}
		seenHost[host.Name] = struct{}{}
	}
	seenMount := make(map[string]struct{})
	seenSerial := make(map[string]struct{})
	for _, disk := range input.Disks {
		if !serialPattern.MatchString(disk.Serial) {
			return fmt.Errorf("invalid data disk serial %q", disk.Serial)
		}
		if !safeMount(disk.Mount) {
			return fmt.Errorf("unsafe data disk mount %q", disk.Mount)
		}
		if disk.Filesystem != "auto" && disk.Filesystem != "xfs" && disk.Filesystem != "ext4" {
			return fmt.Errorf("unsupported filesystem %q", disk.Filesystem)
		}
		if _, exists := seenMount[disk.Mount]; exists {
			return fmt.Errorf("duplicate mount %q", disk.Mount)
		}
		if _, exists := seenSerial[disk.Serial]; exists {
			return fmt.Errorf("duplicate disk serial %q", disk.Serial)
		}
		seenMount[disk.Mount] = struct{}{}
		seenSerial[disk.Serial] = struct{}{}
	}
	seenTags := make(map[string]struct{}, len(input.Shares))
	seenGuests := make(map[string]struct{}, len(input.Shares))
	for _, share := range input.Shares {
		if !shareTagPattern.MatchString(share.Tag) {
			return fmt.Errorf("invalid Farrow share tag %q", share.Tag)
		}
		if !safeShareMount(share.Guest, input.SSHUser) {
			return fmt.Errorf("unsafe guest share mount %q", share.Guest)
		}
		if _, exists := seenTags[share.Tag]; exists {
			return fmt.Errorf("duplicate Farrow share tag %q", share.Tag)
		}
		for existing := range seenGuests {
			if mountPathsOverlap(existing, share.Guest) {
				return fmt.Errorf("overlapping guest share mounts %q and %q", existing, share.Guest)
			}
		}
		for diskMount := range seenMount {
			if mountPathsOverlap(diskMount, share.Guest) {
				return fmt.Errorf("guest share mount %q overlaps data disk mount %q", share.Guest, diskMount)
			}
		}
		seenTags[share.Tag] = struct{}{}
		seenGuests[share.Guest] = struct{}{}
	}
	if input.PrivateKey != "" {
		if !input.Control || len(input.Hosts) < 2 {
			return errors.New("private key may only be injected into a multi-node control guest")
		}
		if strings.ContainsRune(input.PrivateKey, '\x00') {
			return errors.New("private key contains NUL")
		}
		parsedPrivate, err := ssh.ParseRawPrivateKey([]byte(input.PrivateKey))
		if err != nil {
			return fmt.Errorf("parse control private key: %w", err)
		}
		signer, err := ssh.NewSignerFromKey(parsedPrivate)
		if err != nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
			return errors.New("control private key must be Ed25519")
		}
		parsedPublic, _, _, _, err := ssh.ParseAuthorizedKey([]byte(input.PublicKey))
		if err != nil || !bytes.Equal(parsedPublic.Marshal(), signer.PublicKey().Marshal()) {
			return errors.New("control private key does not match the deployment public key")
		}
	}
	return nil
}

func yamlQuote(value string) string { return strconv.Quote(value) }

func indent(content string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderDiskScript(disks []Disk) string {
	var out strings.Builder
	out.WriteString("#!/bin/bash\nset -euo pipefail\n\n")
	out.WriteString(`init_disk() {
  local serial="$1" mountpoint="$2" requested="$3" dev="/dev/disk/by-id/virtio-$1"
  for _ in $(seq 1 60); do
    [[ -b "${dev}" ]] && break
    sleep 1
  done
  [[ -b "${dev}" ]] || { echo "missing Farrow disk ${dev}" >&2; return 1; }
  if ! blkid "${dev}" >/dev/null 2>&1; then
    if [[ "${requested}" == "xfs" ]] || { [[ "${requested}" == "auto" ]] && command -v mkfs.xfs >/dev/null 2>&1; }; then
      mkfs.xfs -f "${dev}"
    elif [[ "${requested}" == "ext4" ]] || [[ "${requested}" == "auto" ]]; then
      command -v mkfs.ext4 >/dev/null 2>&1 || { echo "mkfs.ext4 missing" >&2; return 1; }
      mkfs.ext4 -F "${dev}"
    else
      echo "unsupported filesystem ${requested}" >&2
      return 1
    fi
  fi
  local uuid fstype
	  uuid=$(blkid -s UUID -o value "${dev}")
	  fstype=$(blkid -s TYPE -o value "${dev}")
	  [[ -n "${uuid}" && -n "${fstype}" ]] || { echo "disk metadata missing for ${dev}" >&2; return 1; }
	  mkdir -p "${mountpoint}"
	  local already_mounted=false
	  if mountpoint -q "${mountpoint}"; then
	    already_mounted=true
	    local mounted_source mounted_uuid
	    mounted_source=$(findmnt -n -o SOURCE --target "${mountpoint}")
	    mounted_uuid=$(blkid -s UUID -o value "${mounted_source}")
	    [[ "${mounted_uuid}" == "${uuid}" ]] || { echo "wrong filesystem mounted at ${mountpoint}: ${mounted_source}" >&2; return 1; }
	  fi
	  local fstab_tmp
	  fstab_tmp=$(mktemp /etc/fstab.farrow.XXXXXX)
	  awk -v mountpoint="${mountpoint}" 'NF < 2 || $2 != mountpoint { print }' /etc/fstab > "${fstab_tmp}"
	  printf 'UUID=%s %s %s defaults,nofail 0 2\n' "${uuid}" "${mountpoint}" "${fstype}" >> "${fstab_tmp}"
	  install -o root -g root -m 0644 "${fstab_tmp}" /etc/fstab
	  rm -f -- "${fstab_tmp}"
	  if [[ "${already_mounted}" == false ]]; then
	    mount "${mountpoint}"
	  fi
  if [[ "${fstype}" == "xfs" ]] && command -v xfs_growfs >/dev/null 2>&1; then
    xfs_growfs "${mountpoint}"
  elif [[ "${fstype}" == "ext4" ]] && command -v resize2fs >/dev/null 2>&1; then
    resize2fs "${dev}"
  fi
}

`)
	for _, disk := range disks {
		fmt.Fprintf(&out, "init_disk %s %s %s\n", disk.Serial, disk.Mount, disk.Filesystem)
	}
	return out.String()
}

func renderShareScript(user string, shares []Share) string {
	if len(shares) == 0 {
		return ""
	}
	const (
		beginMarker = "# BEGIN FARROW SHARES"
		endMarker   = "# END FARROW SHARES"
		baseOptions = "version=9p2000.L,trans=virtio,cache=none,msize=262144,access=any,nofail,nodev,nosuid"
	)
	var out strings.Builder
	out.WriteString("#!/bin/bash\nset -Eeuo pipefail\n\n")
	fmt.Fprintf(&out, "share_user=%q\n", user)
	fmt.Fprintf(&out, "begin_marker=%q\n", beginMarker)
	fmt.Fprintf(&out, "end_marker=%q\n", endMarker)
	out.WriteString("is_desired_share() {\n  case \"$1|$2\" in\n")
	for _, share := range shares {
		fmt.Fprintf(&out, "    %q) return 0 ;;\n", share.Tag+"|"+share.Guest)
	}
	out.WriteString("    *) return 1 ;;\n  esac\n}\n\n")
	out.WriteString(`fstab=/etc/fstab
fstab_tmp=
active_probe=

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "${active_probe}" ]]; then
    rm -f -- "${active_probe}" || true
  fi
  if [[ -n "${fstab_tmp}" ]]; then
    rm -f -- "${fstab_tmp}" || true
  fi
  exit "${status}"
}
trap cleanup EXIT

path_overlap() {
  local left="$1" right="$2"
  [[ "${left}" == "${right}" || "${left}" == "${right}"/* || "${right}" == "${left}"/* ]]
}

safe_guest_mount() {
  local path="$1" root canonical
  [[ "${path}" =~ ^/[A-Za-z0-9._/-]+$ && "${path}" != / ]]
  canonical=$(realpath -m -- "${path}")
  [[ "${canonical}" == "${path}" ]]
  for root in /bin /boot /dev /etc /lib /lib64 /proc /root /run /sbin /sys /usr /var/lib/farrow; do
    if path_overlap "${path}" "${root}"; then
      return 1
    fi
  done
  if path_overlap "${path}" "/home/${share_user}/.ssh"; then
    return 1
  fi
  return 0
}

for required in awk findmnt grep mktemp mount mountpoint realpath runuser stat sync umount; do
  command -v "${required}" >/dev/null
done
[[ -f "${fstab}" && ! -L "${fstab}" ]]
[[ "$(stat -c '%u:%g' "${fstab}")" == "0:0" ]]
fstab_mode=$(stat -c '%a' "${fstab}")
[[ "${fstab_mode}" =~ ^[0-7]{3,4}$ ]]
(( (8#${fstab_mode} & 8#22) == 0 ))
begin_count=$(grep -Fxc -- "${begin_marker}" "${fstab}" || true)
end_count=$(grep -Fxc -- "${end_marker}" "${fstab}" || true)
if (( begin_count != end_count || begin_count > 1 )); then
  echo 'malformed Farrow share block in /etc/fstab' >&2
  exit 1
fi

declare -a old_shares=()
if (( begin_count == 1 )); then
  begin_line=$(grep -Fn -- "${begin_marker}" "${fstab}" | cut -d: -f1)
  end_line=$(grep -Fn -- "${end_marker}" "${fstab}" | cut -d: -f1)
  if (( begin_line >= end_line )); then
    echo 'misordered Farrow share block in /etc/fstab' >&2
    exit 1
  fi
  while read -r tag guest fstype options dump pass extra; do
    if [[ -z "${tag}" || -n "${extra:-}" || ! "${tag}" =~ ^farrow-[0-9a-f]{20}$ || "${fstype}" != 9p || "${dump}" != 0 || "${pass}" != 0 ]]; then
      echo 'unsafe entry in Farrow share block' >&2
      exit 1
    fi
    if [[ "${options}" != "version=9p2000.L,trans=virtio,cache=none,msize=262144,access=any,nofail,nodev,nosuid,rw" && "${options}" != "version=9p2000.L,trans=virtio,cache=none,msize=262144,access=any,nofail,nodev,nosuid,ro" ]]; then
      echo 'unexpected options in Farrow share block' >&2
      exit 1
    fi
    safe_guest_mount "${guest}" || { echo "unsafe old Farrow share mount ${guest}" >&2; exit 1; }
    old_shares+=("${tag}|${guest}")
  done < <(awk -v begin="${begin_marker}" -v end="${end_marker}" '
    $0 == begin { inside=1; next }
    $0 == end { inside=0; next }
    inside { print }
  ' "${fstab}")
fi

for old_share in "${old_shares[@]}"; do
  old_tag=${old_share%%|*}
  old_guest=${old_share#*|}
  if ! mountpoint -q "${old_guest}"; then
    continue
  fi
  mounted_source=$(findmnt -n -o SOURCE --target "${old_guest}")
  mounted_type=$(findmnt -n -o FSTYPE --target "${old_guest}")
  if [[ "${mounted_type}" == 9p ]] && is_desired_share "${mounted_source}" "${old_guest}"; then
    continue
  fi
  if [[ "${mounted_source}" != "${old_tag}" || "${mounted_type}" != 9p ]] || is_desired_share "${old_tag}" "${old_guest}"; then
    echo "refuse to unmount unexpected filesystem at ${old_guest}" >&2
    exit 1
  fi
  umount "${old_guest}"
done

fstab_tmp=$(mktemp /etc/.fstab.farrow-shares.XXXXXX)
awk -v begin="${begin_marker}" -v end="${end_marker}" '
  $0 == begin { inside=1; next }
  $0 == end { inside=0; next }
  !inside { print }
' "${fstab}" > "${fstab_tmp}"
printf '%s\n' "${begin_marker}" >> "${fstab_tmp}"
`)
	for _, share := range shares {
		options := baseOptions + ",rw"
		if share.Readonly {
			options = baseOptions + ",ro"
		}
		line := fmt.Sprintf("%s %s 9p %s 0 0", share.Tag, share.Guest, options)
		fmt.Fprintf(&out, "printf '%%s\\n' %q >> \"${fstab_tmp}\"\n", line)
	}
	out.WriteString(`printf '%s\n' "${end_marker}" >> "${fstab_tmp}"
chown root:root "${fstab_tmp}"
chmod 0644 "${fstab_tmp}"
sync -f "${fstab_tmp}"
mv -f -- "${fstab_tmp}" "${fstab}"
fstab_tmp=

has_mount_option() {
  local options="$1" wanted="$2"
  [[ ",${options}," == *,"${wanted}",* ]]
}

verify_mount() {
  local tag="$1" mountpoint="$2" readonly="$3"
  local mounted_source mounted_type mounted_options
  mountpoint -q "${mountpoint}"
  mounted_source=$(findmnt -n -o SOURCE --target "${mountpoint}")
  mounted_type=$(findmnt -n -o FSTYPE --target "${mountpoint}")
  mounted_options=$(findmnt -n -o OPTIONS --target "${mountpoint}")
  [[ "${mounted_source}" == "${tag}" && "${mounted_type}" == 9p ]]
  has_mount_option "${mounted_options}" nodev
  has_mount_option "${mounted_options}" nosuid
  if [[ "${readonly}" == true ]]; then
    has_mount_option "${mounted_options}" ro
  else
    has_mount_option "${mounted_options}" rw
  fi
}

probe_share() {
  local mountpoint="$1" readonly="$2"
  if [[ "${readonly}" == true ]]; then
    if active_probe=$(runuser -u "${share_user}" -- mktemp "${mountpoint}/.farrow-write-probe.XXXXXX" 2>/dev/null); then
      if ! rm -f -- "${active_probe}" || [[ -e "${active_probe}" || -L "${active_probe}" ]]; then
        echo "failed to clean unexpected read-only share probe ${active_probe}" >&2
        return 1
      fi
      active_probe=
      echo "read-only Farrow share is writable by ${share_user}: ${mountpoint}" >&2
      return 1
    fi
    active_probe=
    return 0
  fi

  active_probe=$(runuser -u "${share_user}" -- mktemp "${mountpoint}/.farrow-write-probe.XXXXXX")
  runuser -u "${share_user}" -- /bin/sh -c 'printf "%s\n" farrow-share-probe > "$1"' farrow-probe "${active_probe}"
  runuser -u "${share_user}" -- rm -f -- "${active_probe}"
  [[ ! -e "${active_probe}" && ! -L "${active_probe}" ]]
  active_probe=
}

init_share() {
  local tag="$1" mountpoint="$2" readonly="$3"
  safe_guest_mount "${mountpoint}"
  if [[ -e "${mountpoint}" || -L "${mountpoint}" ]]; then
    [[ -d "${mountpoint}" && ! -L "${mountpoint}" ]]
  else
    install -d -o root -g root -m 0755 "${mountpoint}"
  fi
  [[ "$(readlink -f -- "${mountpoint}")" == "${mountpoint}" ]]
  if mountpoint -q "${mountpoint}"; then
    verify_mount "${tag}" "${mountpoint}" "${readonly}"
    probe_share "${mountpoint}" "${readonly}"
    return 0
  fi
  if find "${mountpoint}" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
    echo "refuse non-empty Farrow share mountpoint ${mountpoint}" >&2
    return 1
  fi
  mount "${mountpoint}"
  verify_mount "${tag}" "${mountpoint}" "${readonly}"
  probe_share "${mountpoint}" "${readonly}"
}

`)
	for _, share := range shares {
		fmt.Fprintf(&out, "init_share %q %q %t\n", share.Tag, share.Guest, share.Readonly)
	}
	return out.String()
}

func renderHostsScript(hosts []Host) string {
	sorted := append([]Host(nil), hosts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var out strings.Builder
	out.WriteString("#!/bin/bash\nset -euo pipefail\n")
	out.WriteString("sed -i.bak '/# farrow-project-host$/d' /etc/hosts\n")
	for _, host := range sorted {
		fmt.Fprintf(&out, "printf '%%s %%s # farrow-project-host\\n' %s %s >> /etc/hosts\n", host.Address, host.Name)
	}
	return out.String()
}

func renderNetworkCheckScript() string {
	return `#!/bin/bash
set -euo pipefail
exec 3<>/dev/tcp/example.com/80
printf 'HEAD / HTTP/1.0\r\nHost: example.com\r\n\r\n' >&3
IFS= read -r response <&3
[[ "${response}" == HTTP/* ]]
printf '%s\n' "${response}"
`
}

func renderPrivateContractScript(network PrivateNetwork) string {
	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail
expected=%q
expected_host=%q
ip link show dev private0 >/dev/null
ip link show dev private0 | grep -Eq '<[^>]*UP[^>]*>'
ip -4 -o address show dev private0 scope global | awk '{print $4}' | grep -Fxq "${expected}"
if ip -4 route show default | grep -Eq '(^|[[:space:]])dev[[:space:]]+private0([[:space:]]|$)'; then
  echo 'private0 owns a default route' >&2
  exit 1
fi
if command -v resolvectl >/dev/null 2>&1; then
  dns=$(resolvectl dns private0 2>/dev/null || true)
  dns=${dns#*:}
	  dns=$(printf '%%s' "${dns}" | tr -d '[:space:]')
  if [[ -n "${dns}" && "${dns}" != "-" ]]; then
    echo "private0 has DNS: ${dns}" >&2
    exit 1
  fi
elif command -v nmcli >/dev/null 2>&1; then
  dns=$(nmcli -g IP4.DNS device show private0 2>/dev/null || true)
  if [[ -n "${dns}" ]]; then
    echo "private0 has DNS: ${dns}" >&2
    exit 1
  fi
fi
printf 'farrow-arp-refresh\n' >"/dev/udp/${expected_host}/9"
printf 'private0 %%s is up with no default route or DNS\n' "${expected}"
`, fmt.Sprintf("%s/%d", network.Address, network.Prefix), network.HostAddress)
}

func renderControlSSHInstallScript(user string) string {
	keySource := "/var/lib/farrow/control-id_ed25519"
	configSource := "/var/lib/farrow/control-ssh-config"
	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail
user=%q
entry=$(getent passwd "${user}")
IFS=: read -r _ _ uid gid _ home _ <<<"${entry}"
[[ "${uid}" =~ ^[0-9]+$ && "${gid}" =~ ^[0-9]+$ ]]
[[ "${home}" == /home/* && -d "${home}" && ! -L "${home}" ]]
[[ "$(readlink -f -- "${home}")" == "${home}" ]]
ssh_dir="${home}/.ssh"
if [[ -e "${ssh_dir}" || -L "${ssh_dir}" ]]; then
  [[ -d "${ssh_dir}" && ! -L "${ssh_dir}" ]]
fi
[[ "$(stat -c '%%u:%%g:%%a' %[2]s)" == "0:0:600" && ! -L %[2]s ]]
[[ "$(stat -c '%%u:%%g:%%a' %[3]s)" == "0:0:600" && ! -L %[3]s ]]
install -d -o "${uid}" -g "${gid}" -m 0700 "${ssh_dir}"
install -o "${uid}" -g "${gid}" -m 0600 %[2]s "${ssh_dir}/id_ed25519"
install -o "${uid}" -g "${gid}" -m 0600 %[3]s "${ssh_dir}/config"
rm -f -- %[2]s %[3]s
[[ "$(stat -c '%%u:%%g:%%a' "${ssh_dir}/id_ed25519")" == "${uid}:${gid}:600" ]]
[[ "$(stat -c '%%u:%%g:%%a' "${ssh_dir}/config")" == "${uid}:${gid}:600" ]]
`, user, keySource, configSource)
}

func renderIdentityContractScript(user string) string {
	var contract string
	if user == "dba" {
		contract = `[[ "${uid}" == 88 && "${gid}" == 88 ]]
[[ "${home}" == /home/dba && "${shell}" == /bin/bash ]]
group=$(getent group admin)
IFS=: read -r group_name _ group_gid _ <<<"${group}"
[[ "${group_name}" == admin && "${group_gid}" == 88 ]]
[[ "$(stat -c '%u:%g' "${home}")" == 88:88 ]]
`
	} else {
		contract = `[[ "${uid}" =~ ^[0-9]+$ && "${gid}" =~ ^[0-9]+$ ]]
[[ "${home}" == /home/* && "${shell}" == /bin/bash ]]
`
	}
	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail
user=%q
entry=$(getent passwd "${user}")
IFS=: read -r _ _ uid gid _ home shell <<<"${entry}"
%s[[ -d "${home}" && ! -L "${home}" ]]
printf 'login identity %%s uid=%%s gid=%%s home=%%s\n' "${user}" "${uid}" "${gid}" "${home}"
`, user, contract)
}

func renderFinalizeScript(controlSSH, privateNetwork, shares bool) string {
	var out strings.Builder
	out.WriteString("#!/bin/bash\nset -euo pipefail\n")
	if shares {
		out.WriteString(`install -d -o root -g root -m 0755 /var/lib/farrow
failure_line=0
trap 'failure_line=${LINENO}' ERR
finalize_exit() {
  local status=$?
  local error_tmp=
  trap - ERR EXIT
  if (( status != 0 )); then
    if error_tmp=$(mktemp /var/lib/farrow/error.json.XXXXXX); then
      if printf '{"exit_status":%d,"line":%d}\n' "${status}" "${failure_line}" > "${error_tmp}" &&
        chown root:root "${error_tmp}" && chmod 0644 "${error_tmp}"; then
        mv -f -- "${error_tmp}" /var/lib/farrow/error.json || rm -f -- "${error_tmp}" || true
      else
        rm -f -- "${error_tmp}" || true
      fi
    fi
  fi
`)
		if controlSSH {
			out.WriteString("  rm -f -- /var/lib/farrow/control-id_ed25519 /var/lib/farrow/control-ssh-config || true\n")
		}
		out.WriteString(`  exit "${status}"
}
trap finalize_exit EXIT
rm -f -- /var/lib/farrow/ready.json /var/lib/farrow/error.json
`)
	} else if controlSSH {
		out.WriteString("trap 'rm -f -- /var/lib/farrow/control-id_ed25519 /var/lib/farrow/control-ssh-config' EXIT\n")
	}
	out.WriteString("/usr/local/libexec/farrow-identity-contract\n")
	out.WriteString("/usr/local/libexec/farrow-hosts\n")
	out.WriteString("/usr/local/libexec/farrow-network-check\n")
	out.WriteString("/usr/local/libexec/farrow-init-disks\n")
	if shares {
		out.WriteString("/usr/local/libexec/farrow-init-shares\n")
	}
	if controlSSH {
		out.WriteString("/usr/local/libexec/farrow-install-control-ssh\n")
	}
	if privateNetwork {
		out.WriteString("/usr/local/libexec/farrow-private-contract\n")
	}
	out.WriteString("/usr/local/libexec/farrow-ready\n")
	return out.String()
}

func renderReadyScript(input Input) (string, error) {
	ready := struct {
		Node       string `json:"node"`
		Generation uint64 `json:"generation"`
		SpecHash   string `json:"spec_hash"`
	}{input.Node, input.Generation, input.SpecHash}
	data, err := json.Marshal(ready)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ninstall -d -m 0755 /var/lib/farrow\nprintf '%%s\\n' %s > /var/lib/farrow/ready.json.tmp\nchmod 0644 /var/lib/farrow/ready.json.tmp\nmv /var/lib/farrow/ready.json.tmp /var/lib/farrow/ready.json\n", yamlQuote(string(data))), nil
}

func renderMetaData(input Input) []byte {
	return []byte(fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", yamlQuote(fmt.Sprintf("farrow-%s-g%d", input.Node, input.Generation)), yamlQuote(input.Hostname)))
}

func renderNetwork(input Input) []byte {
	var out strings.Builder
	out.WriteString("version: 2\nethernets:\n  management:\n    match:\n      macaddress: ")
	out.WriteString(yamlQuote(strings.ToLower(input.MgmtMAC)))
	out.WriteString("\n    set-name: mgmt0\n    dhcp4: true\n    dhcp6: false\n    dhcp4-overrides:\n      route-metric: 100\n")
	if input.Private != nil {
		out.WriteString("  private:\n    match:\n      macaddress: ")
		out.WriteString(yamlQuote(strings.ToLower(input.Private.MAC)))
		out.WriteString("\n    set-name: private0\n    dhcp4: false\n    dhcp6: false\n    accept-ra: false\n    link-local: []\n    addresses:\n      - ")
		out.WriteString(yamlQuote(fmt.Sprintf("%s/%d", input.Private.Address, input.Private.Prefix)))
		out.WriteString("\n")
	}
	return []byte(out.String())
}

func renderUserData(input Input) ([]byte, error) {
	readyScript, err := renderReadyScript(input)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("#cloud-config\npreserve_hostname: false\nhostname: ")
	out.WriteString(yamlQuote(input.Hostname))
	if input.SSHUser == "dba" {
		out.WriteString("\nbootcmd:\n  - ['/bin/sh', '-c', 'if /usr/bin/getent group admin >/dev/null; then current=$(/usr/bin/getent group admin | /usr/bin/cut -d: -f3); if test \"${current}\" != 88; then ! /usr/bin/getent group 88 >/dev/null; /usr/sbin/groupmod --gid 88 admin; fi; else ! /usr/bin/getent group 88 >/dev/null; /usr/sbin/groupadd --gid 88 admin; fi']")
	}
	out.WriteString("\nssh_pwauth: false\ndisable_root: true\nssh_deletekeys: false\nusers:\n  - default\n  - name: ")
	out.WriteString(yamlQuote(input.SSHUser))
	if input.SSHUser == "dba" {
		out.WriteString("\n    uid: 88\n    primary_group: admin\n    create_groups: false")
	}
	out.WriteString("\n    shell: /bin/bash\n    lock_passwd: true\n    sudo:\n      - ALL=(ALL) NOPASSWD:ALL\n    ssh_authorized_keys:\n      - ")
	out.WriteString(yamlQuote(input.PublicKey))
	out.WriteString("\ngrowpart:\n  mode: auto\n  devices: ['/']\nresize_rootfs: true\nntp:\n  enabled: true\nwrite_files:\n")

	writeFile := func(path, owner, permissions, content string) {
		out.WriteString("  - path: ")
		out.WriteString(yamlQuote(path))
		out.WriteString("\n    owner: ")
		out.WriteString(yamlQuote(owner))
		out.WriteString("\n    permissions: ")
		out.WriteString(yamlQuote(permissions))
		out.WriteString("\n    content: |\n")
		out.WriteString(indent(content, 6))
	}
	writeFile("/usr/local/libexec/farrow-init-disks", "root:root", "0755", renderDiskScript(input.Disks))
	if len(input.Shares) != 0 {
		writeFile("/usr/local/libexec/farrow-init-shares", "root:root", "0755", renderShareScript(input.SSHUser, input.Shares))
	}
	writeFile("/usr/local/libexec/farrow-hosts", "root:root", "0755", renderHostsScript(input.Hosts))
	writeFile("/usr/local/libexec/farrow-network-check", "root:root", "0755", renderNetworkCheckScript())
	writeFile("/usr/local/libexec/farrow-identity-contract", "root:root", "0755", renderIdentityContractScript(input.SSHUser))
	if input.Private != nil {
		writeFile("/usr/local/libexec/farrow-private-contract", "root:root", "0755", renderPrivateContractScript(*input.Private))
	}
	writeFile("/usr/local/libexec/farrow-ready", "root:root", "0755", readyScript)
	if input.PrivateKey != "" {
		// write_files runs before users-groups on supported cloud-init images.
		// Stage the secret as root, then install it only after the login user
		// exists. Referring to the future user as write_files owner makes the
		// entire module fail and can leave a deceptively usable guest.
		writeFile("/var/lib/farrow/control-id_ed25519", "root:root", "0600", input.PrivateKey)
		sshConfig := "Host " + strings.Join(hostNames(input.Hosts), " ") + "\n  User " + input.SSHUser + "\n  IdentityFile ~/.ssh/id_ed25519\n  StrictHostKeyChecking accept-new\n"
		writeFile("/var/lib/farrow/control-ssh-config", "root:root", "0600", sshConfig)
		writeFile("/usr/local/libexec/farrow-install-control-ssh", "root:root", "0700", renderControlSSHInstallScript(input.SSHUser))
	}
	writeFile("/usr/local/libexec/farrow-finalize", "root:root", "0755", renderFinalizeScript(input.PrivateKey != "", input.Private != nil, len(input.Shares) != 0))
	out.WriteString("runcmd:\n  - ['/usr/local/libexec/farrow-finalize']\n")
	return []byte(out.String()), nil
}

func hostNames(hosts []Host) []string {
	names := make([]string, 0, len(hosts))
	for _, host := range hosts {
		names = append(names, host.Name)
	}
	sort.Strings(names)
	return names
}

// Render validates all interpolated values before producing YAML/shell text.
func Render(input Input) (Files, error) {
	if err := validateInput(input); err != nil {
		return Files{}, err
	}
	userData, err := renderUserData(input)
	if err != nil {
		return Files{}, err
	}
	files := Files{MetaData: renderMetaData(input), UserData: userData, NetworkConfig: renderNetwork(input)}
	if bytes.Contains(files.UserData, []byte("StrictHostKeyChecking no")) {
		return Files{}, errors.New("unsafe SSH host key configuration generated")
	}
	return files, nil
}
