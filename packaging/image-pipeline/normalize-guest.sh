#!/bin/bash
set -euo pipefail

# This script runs only inside virt-customize's offline appliance. It accepts
# deliberately narrow, pre-validated values; it never downloads packages or
# reads credentials from the host.
if [[ $# -ne 2 ]]; then
  printf 'usage: %s <source-user> <source-date-epoch>\n' "$0" >&2
  exit 2
fi
source_user=$1
source_date_epoch=$2
[[ ${source_user} =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || { printf 'invalid source user\n' >&2; exit 2; }
[[ ${source_date_epoch} =~ ^[0-9]{1,10}$ ]] || { printf 'invalid source epoch\n' >&2; exit 2; }
export LC_ALL=C
export TZ=UTC
PATH=/usr/sbin:/usr/bin:/sbin:/bin

for command in awk cat chmod chown find getent groupadd groupmod install ln rm stat touch useradd usermod; do
  command -v "${command}" >/dev/null || { printf 'required guest command missing: %s\n' "${command}" >&2; exit 3; }
done

# Fail closed rather than silently reassigning an unrelated numeric identity.
if getent group admin >/dev/null; then
  admin_gid=$(getent group admin | awk -F: 'NR == 1 { print $3 }')
  if [[ ${admin_gid} != 88 ]]; then
    ! getent group 88 >/dev/null || { printf 'GID 88 is occupied by a group other than admin\n' >&2; exit 4; }
    groupmod --gid 88 admin
  fi
else
  ! getent group 88 >/dev/null || { printf 'GID 88 is occupied and admin is absent\n' >&2; exit 4; }
  groupadd --gid 88 admin
fi

if getent passwd dba >/dev/null; then
  dba_uid=$(getent passwd dba | awk -F: 'NR == 1 { print $3 }')
  if [[ ${dba_uid} != 88 ]]; then
    ! getent passwd 88 >/dev/null || { printf 'UID 88 is occupied by a user other than dba\n' >&2; exit 4; }
    usermod --uid 88 dba
  fi
else
  ! getent passwd 88 >/dev/null || { printf 'UID 88 is occupied and dba is absent\n' >&2; exit 4; }
  useradd --uid 88 --gid 88 --home-dir /home/dba --shell /bin/bash --no-user-group dba
fi
usermod --gid 88 --home /home/dba --shell /bin/bash --password '!' dba
install -d -o dba -g admin -m 0700 /home/dba /home/dba/.ssh
chown -R 88:88 /home/dba

# Remove authentication material from every conventional home/skel location.
# This is intentionally stronger than deleting one known development key.
for root in /root /home /etc/skel; do
  [[ -e ${root} ]] || continue
  find "${root}" -xdev -type f \( \
    -name authorized_keys -o -name authorized_keys2 -o \
    -name 'id_rsa*' -o -name 'id_dsa*' -o -name 'id_ecdsa*' -o -name 'id_ed25519*' -o \
    -name known_hosts -o -name known_hosts.old -o \
    -name .bash_history -o -name .zsh_history -o -name .mysql_history -o -name .psql_history \
  \) -delete
done

# Replace legacy password hashes, rather than merely prefixing a still-present
# hash. Lock root, the declared bootstrap user, dba, and all normal UID>=1000
# accounts. Duplicate names are harmless.
mapfile -t normal_users < <(awk -F: '$3 >= 1000 { print $1 }' /etc/passwd)
for user in root "${source_user}" dba "${normal_users[@]}"; do
  [[ -n ${user} ]] || continue
  getent passwd "${user}" >/dev/null || continue
  usermod --password '!' "${user}"
done

install -d -o root -g root -m 0755 /etc/sudoers.d
printf 'dba ALL=(ALL) NOPASSWD: ALL\n' >/etc/sudoers.d/90-piglet-dba
chown root:root /etc/sudoers.d/90-piglet-dba
chmod 0440 /etc/sudoers.d/90-piglet-dba
if command -v visudo >/dev/null; then
  visudo -cf /etc/sudoers >/dev/null
fi

install -d -o root -g root -m 0755 /etc/ssh/sshd_config.d
cat >/etc/ssh/sshd_config.d/99-piglet-image.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PermitRootLogin no
PubkeyAuthentication yes
EOF
chmod 0644 /etc/ssh/sshd_config.d/99-piglet-image.conf
rm -f -- /etc/ssh/ssh_host_*

: >/etc/machine-id
if [[ -d /var/lib/dbus ]]; then
  rm -f -- /var/lib/dbus/machine-id
  ln -s /etc/machine-id /var/lib/dbus/machine-id
fi
rm -f -- /var/lib/systemd/random-seed
if [[ -d /var/lib/cloud ]]; then
  find /var/lib/cloud -mindepth 1 -delete
fi
rm -f -- /var/log/cloud-init.log /var/log/cloud-init-output.log

# Verify the security/identity postconditions inside the guest before writing
# the completion marker consumed by the host pipeline.
getent passwd dba | awk -F: '$1 == "dba" && $3 == 88 && $4 == 88 && $6 == "/home/dba" && $7 == "/bin/bash" { ok=1 } END { exit !ok }'
getent group admin | awk -F: '$1 == "admin" && $3 == 88 { ok=1 } END { exit !ok }'
[[ $(stat -c '%u:%g:%a' /home/dba) == 88:88:700 ]]
for user in root "${source_user}" dba "${normal_users[@]}"; do
  [[ -n ${user} ]] || continue
  getent passwd "${user}" >/dev/null || continue
  [[ $(getent shadow "${user}" | awk -F: 'NR == 1 { print $2 }') == '!' ]]
done
for root in /root /home /etc/skel; do
  [[ -e ${root} ]] || continue
  residual=$(find "${root}" -xdev -type f \( \
    -name authorized_keys -o -name authorized_keys2 -o \
    -name 'id_rsa*' -o -name 'id_dsa*' -o -name 'id_ecdsa*' -o -name 'id_ed25519*' \
  \) -print -quit)
  [[ -z ${residual} ]] || { printf 'residual SSH credential: %s\n' "${residual}" >&2; exit 5; }
done
residual=$(find /etc/ssh -maxdepth 1 -type f -name 'ssh_host_*' -print -quit)
[[ -z ${residual} ]] || { printf 'residual SSH host key: %s\n' "${residual}" >&2; exit 5; }
[[ ! -s /etc/machine-id ]]
if [[ -d /var/lib/cloud ]]; then
  residual=$(find /var/lib/cloud -mindepth 1 -print -quit)
  [[ -z ${residual} ]] || { printf 'residual cloud-init cache: %s\n' "${residual}" >&2; exit 5; }
fi

install -d -o root -g root -m 0755 /var/lib/piglet-image
printf '{"schema":1,"recipe":"piglet-official-image-normalization-v1","source_user":"%s","source_date_epoch":%s,"dba_uid":88,"admin_gid":88,"credential_hygiene":"applied"}\n' \
  "${source_user}" "${source_date_epoch}" >/var/lib/piglet-image/normalization.json
chmod 0644 /var/lib/piglet-image/normalization.json

# Normalize the metadata of files created by this recipe. Filesystem journals
# and allocation are still part of the pinned native toolchain boundary; the
# host records the exact output digest and requires a repeat build comparison.
touch -d "@${source_date_epoch}" \
  /home/dba /home/dba/.ssh \
  /etc/machine-id \
  /etc/sudoers.d/90-piglet-dba \
  /etc/ssh/sshd_config.d/99-piglet-image.conf \
  /var/lib/piglet-image /var/lib/piglet-image/normalization.json

# EL guests enforce SELinux labels. A blanket virt-customize
# --selinux-relabel is not valid for the Debian/Ubuntu inputs, so relabel only
# the paths this cross-distribution recipe creates or replaces when restorecon
# is present. Native boot/readiness remains a promotion gate.
if command -v restorecon >/dev/null; then
  restorecon -F /etc/passwd /etc/group /etc/shadow /etc/gshadow /etc/machine-id
  restorecon -RF /home/dba /etc/sudoers.d/90-piglet-dba \
    /etc/ssh/sshd_config.d/99-piglet-image.conf /var/lib/piglet-image
  [[ ! -e /var/lib/dbus/machine-id ]] || restorecon -F /var/lib/dbus/machine-id
fi
