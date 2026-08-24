# M0-B privileged handoff plan — 2026-08-23

Historical handoff: this exact plan was subsequently revalidated and executed.
The sudo blocker is closed. Root-owned target hashes/modes, launchd status,
host-mode two-VM reachability, and daemon restart/reconnect passed. See
[`m0-b-darwin-private-native-20260823.md`](m0-b-darwin-private-native-20260823.md).
The commands below remain the pre-execution audit record, not current blocker
text.

## Revalidated inputs

```text
stage: /Users/vonng/Library/Caches/piglet/socket_vmnet/v1.2.2/stage-lease-ZisEE5
interface ID: 89e9f9e5-60cb-48a0-a739-b7fa6e49cde6
daemon SHA-256: b8a72a62237312f2f756027dea504a844edeb40014702d4a320292c026d282b0
client SHA-256: 2d2e364b808f0b43a92bdd7ac3c7b390d6b55a33c761c61c0738d688286a3eff
plist: plutil OK
```

All destination paths are currently absent. Immediately before install they
must be checked again for symlinks, ownership, mode, and unexpected adoption.

## Root-owned system-tool operations

No user-writable script or Piglet binary is executed as root. The only root
programs are macOS `/usr/bin/install`, `/usr/sbin/chown`, `/bin/chmod`, and
`/bin/launchctl`, each with explicit paths:

```text
sudo -n /usr/bin/install -d -o root -g wheel -m 0755 /opt/piglet
sudo -n /usr/bin/install -d -o root -g wheel -m 0755 /opt/piglet/libexec
sudo -n /usr/bin/install -d -o root -g wheel -m 0700 /private/var/db/piglet
sudo -n /usr/bin/install -d -o root -g wheel -m 0755 /var/log/piglet-vmnet
sudo -n /usr/bin/install -d -o root -g wheel -m 1777 /private/var/run/piglet

sudo -n /usr/bin/install -o root -g wheel -m 0755 <stage>/socket_vmnet /opt/piglet/libexec/socket_vmnet
sudo -n /usr/bin/install -o root -g wheel -m 0755 <stage>/socket_vmnet_client /opt/piglet/libexec/socket_vmnet_client
sudo -n /usr/bin/install -o root -g wheel -m 0600 <stage>/network.json /private/var/db/piglet/network.json
sudo -n /usr/bin/install -o root -g wheel -m 0644 <stage>/io.pgsty.piglet.vmnet.plist /Library/LaunchDaemons/io.pgsty.piglet.vmnet.plist

sudo -n /bin/launchctl bootstrap system /Library/LaunchDaemons/io.pgsty.piglet.vmnet.plist
sudo -n /bin/launchctl enable system/io.pgsty.piglet.vmnet
sudo -n /bin/launchctl kickstart -kp system/io.pgsty.piglet.vmnet
```

After copying and before launch, the destination binary hashes and every
parent component's owner/mode/symlink status must be reverified.

## Rollback boundary

Rollback is allowed only after proving no private VM/lease is active and every
installed file still matches this manifest. It boots out only
`system/io.pgsty.piglet.vmnet`, removes the exact matching plist/state/binaries,
removes socket/pid only after the daemon is gone, and removes Piglet directories
only when empty. `/private/var/run/piglet` may be removed only when its exact
lease/lock are absent and the directory remains root:wheel mode 1777. No
pre-existing state needs restoration because preflight
proved all targets absent.
