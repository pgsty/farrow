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

type PrivateNetwork struct {
	MAC         string
	Address     string
	Prefix      int
	HostAddress string
}

type Input struct {
	ProjectID  string
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
	hashPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	userPattern     = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	mountPattern    = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	uuidPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

func reservedMount(path string) bool {
	for _, root := range []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/piglet"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func safeMount(path string) bool {
	return mountPattern.MatchString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && !reservedMount(path)
}

func singleLine(label, value string) error {
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must be a non-empty single line", label)
	}
	return nil
}

func validateInput(input Input) error {
	if !uuidPattern.MatchString(input.ProjectID) {
		return errors.New("project ID must be a lowercase UUID")
	}
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
			return errors.New("control private key does not match the project public key")
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
  [[ -b "${dev}" ]] || { echo "missing Piglet disk ${dev}" >&2; return 1; }
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
	  fstab_tmp=$(mktemp /etc/fstab.piglet.XXXXXX)
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

func renderHostsScript(hosts []Host) string {
	sorted := append([]Host(nil), hosts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var out strings.Builder
	out.WriteString("#!/bin/bash\nset -euo pipefail\n")
	out.WriteString("sed -i.bak '/# piglet-project-host$/d' /etc/hosts\n")
	for _, host := range sorted {
		fmt.Fprintf(&out, "printf '%%s %%s # piglet-project-host\\n' %s %s >> /etc/hosts\n", host.Address, host.Name)
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
printf 'piglet-arp-refresh\n' >"/dev/udp/${expected_host}/9"
printf 'private0 %%s is up with no default route or DNS\n' "${expected}"
`, fmt.Sprintf("%s/%d", network.Address, network.Prefix), network.HostAddress)
}

func renderControlSSHInstallScript(user string) string {
	keySource := "/var/lib/piglet/control-id_ed25519"
	configSource := "/var/lib/piglet/control-ssh-config"
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

func renderFinalizeScript(controlSSH, privateNetwork bool) string {
	var out strings.Builder
	out.WriteString("#!/bin/bash\nset -euo pipefail\n")
	if controlSSH {
		out.WriteString("trap 'rm -f -- /var/lib/piglet/control-id_ed25519 /var/lib/piglet/control-ssh-config' EXIT\n")
	}
	out.WriteString("/usr/local/libexec/piglet-identity-contract\n")
	out.WriteString("/usr/local/libexec/piglet-hosts\n")
	out.WriteString("/usr/local/libexec/piglet-init-disks\n")
	if controlSSH {
		out.WriteString("/usr/local/libexec/piglet-install-control-ssh\n")
	}
	if privateNetwork {
		out.WriteString("/usr/local/libexec/piglet-private-contract\n")
	}
	out.WriteString("/usr/local/libexec/piglet-ready\n")
	return out.String()
}

func renderReadyScript(input Input) (string, error) {
	ready := struct {
		Project    string `json:"project"`
		Node       string `json:"node"`
		Generation uint64 `json:"generation"`
		SpecHash   string `json:"spec_hash"`
	}{input.ProjectID, input.Node, input.Generation, input.SpecHash}
	data, err := json.Marshal(ready)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ninstall -d -m 0755 /var/lib/piglet\nprintf '%%s\\n' %s > /var/lib/piglet/ready.json.tmp\nchmod 0644 /var/lib/piglet/ready.json.tmp\nmv /var/lib/piglet/ready.json.tmp /var/lib/piglet/ready.json\n", yamlQuote(string(data))), nil
}

func renderMetaData(input Input) []byte {
	return []byte(fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", yamlQuote(fmt.Sprintf("piglet-%s-%s-g%d", input.ProjectID, input.Node, input.Generation)), yamlQuote(input.Hostname)))
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
	writeFile("/usr/local/libexec/piglet-init-disks", "root:root", "0755", renderDiskScript(input.Disks))
	writeFile("/usr/local/libexec/piglet-hosts", "root:root", "0755", renderHostsScript(input.Hosts))
	writeFile("/usr/local/libexec/piglet-network-check", "root:root", "0755", renderNetworkCheckScript())
	writeFile("/usr/local/libexec/piglet-identity-contract", "root:root", "0755", renderIdentityContractScript(input.SSHUser))
	if input.Private != nil {
		writeFile("/usr/local/libexec/piglet-private-contract", "root:root", "0755", renderPrivateContractScript(*input.Private))
	}
	writeFile("/usr/local/libexec/piglet-ready", "root:root", "0755", readyScript)
	if input.PrivateKey != "" {
		// write_files runs before users-groups on supported cloud-init images.
		// Stage the secret as root, then install it only after the login user
		// exists. Referring to the future user as write_files owner makes the
		// entire module fail and can leave a deceptively usable guest.
		writeFile("/var/lib/piglet/control-id_ed25519", "root:root", "0600", input.PrivateKey)
		sshConfig := "Host " + strings.Join(hostNames(input.Hosts), " ") + "\n  User " + input.SSHUser + "\n  IdentityFile ~/.ssh/id_ed25519\n  StrictHostKeyChecking accept-new\n"
		writeFile("/var/lib/piglet/control-ssh-config", "root:root", "0600", sshConfig)
		writeFile("/usr/local/libexec/piglet-install-control-ssh", "root:root", "0700", renderControlSSHInstallScript(input.SSHUser))
	}
	writeFile("/usr/local/libexec/piglet-finalize", "root:root", "0755", renderFinalizeScript(input.PrivateKey != "", input.Private != nil))
	out.WriteString("runcmd:\n  - ['/usr/local/libexec/piglet-finalize']\n")
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
