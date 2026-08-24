#!/usr/bin/env bash
set -euo pipefail

uname -a
uname -m

if command -v sw_vers >/dev/null 2>&1; then
  sw_vers
fi

go version

for binary in qemu-system-aarch64 qemu-system-x86_64 qemu-img; do
  if path=$(command -v "${binary}" 2>/dev/null); then
    "${path}" --version | head -n 2
  else
    printf '%s: absent\n' "${binary}"
  fi
done

df -h .

if command -v ifconfig >/dev/null 2>&1; then
  ifconfig -l
fi

if command -v netstat >/dev/null 2>&1; then
  netstat -rn -f inet 2>/dev/null | awk 'NR == 1 || /10\.10\.10\./ || /^default/'
fi

for path in \
  /opt/piglet \
  /Library/LaunchDaemons/io.pgsty.piglet.vmnet.plist \
  /private/var/run/piglet-vmnet.sock \
  /private/var/db/piglet/network.json; do
  if [[ -e "${path}" ]]; then
    stat -f '%Sp %Su:%Sg %N' "${path}" 2>/dev/null || stat "${path}"
  else
    printf '%s: absent\n' "${path}"
  fi
done
