# Linux private network plan foundation — 2026-08-23

Result class: unit, race, Staticcheck, and Linux compile-only. No Linux host,
systemd bridge, helper attach, or KVM VM was run.

The typed plan implements ADR-0007's bounded systemd-networkd path:

- root-owned `piglet0` netdev/network units with host `/24` address, no DHCP or
  NAT contract;
- exact standalone bridge.conf marker block preserving unrelated rules;
- NetworkManager unmanaged drop-in only when detected;
- tmpfiles root:root mode-1777 `/run/piglet` for the host-global lease;
- Debian-only typed `dpkg-statoverride --update --add root kvm 4750` after
  proving no non-Piglet override;
- RPM mode-4755 verification with an explicit local-user attach warning and no
  mutation;
- strict root-owned ownership manifest with every generated content hash,
  original bridge.conf, original helper state, and applied override.

Uninstall planning refuses active lease, bridge members, missing/changed files,
modified helper/override, malformed/trailing/unknown manifest data, and
unowned bridge adoption. A valid uninstall restores original bridge.conf,
removes the exact override, explicitly restores helper owner/group/mode, and
lists only manifest-owned files/directories.

Twenty repeated unit runs and ten race runs passed; Staticcheck passed; Linux
amd64 and arm64 test binaries compiled as static ELF. These results prove plan
logic only. The required zero-preconfiguration Ubuntu 24.04 systemd/KVM native
install, bridge-helper attach smoke, dual-NIC two-VM lifecycle, and uninstall
remain blocked by the missing runner.
