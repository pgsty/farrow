# Linux product network executor — 2026-08-24

Result class: native real product E2E on clean Ubuntu 24.04. This follow-up
replaced the audited manual handoff with public `piglet network` commands.

## Identity and commands

- Host: `vonng-aimax`, Ubuntu 24.04.3 LTS amd64, KVM/QEMU 8.2.2.
- Product binary SHA-256:
  `32a905e600719d55ea82d632a154ca8ba4895dc686a23e1ab5346833ae1adbd2`.
- Repository: unborn `main`; isolated source snapshot under
  `/data/piglet-v1-e2e-20260823-2330/src`.

The native sequence was:

```text
piglet network install --json
piglet network install --yes --json
piglet network install --yes --json       # repeated install
piglet network status --json
piglet start --json                       # existing private project
piglet network uninstall --yes --json     # correctly refused: active lease
piglet stop --json
piglet network uninstall --json
piglet network uninstall --yes --json
```

## Install behavior verified

- Default install was read-only and emitted exact directories, files,
  owner/modes, content, command argv, original helper metadata, and four-unit
  networkd prestate.
- `--yes` invoked only fixed `/usr/bin/sudo -n -- <absolute-system-binary>
  argv. No shell string or user-writable executable ran as root.
- Discovery rejected unowned target adoption and required package-owned,
  root-owned, regular non-symlink helper plus safe parents.
- NM's dedicated unmanaged rule loaded before bridge creation.
- State was persisted before service/helper mutations; files came from a
  mode-0700 generated staging tree and were rehashed immediately before exact
  root install.
- Networkd started non-persistently, produced `piglet0=10.10.10.1/24`, and the
  reversible dpkg override produced root:kvm 4750.
- A non-root, diskless, paused QEMU/KVM process attached through the helper;
  QMP name/UUID and process argv identity were verified, a real tap member was
  observed, and QMP quit left no QEMU/tap/runtime residue.
- Only after that smoke did the executor enable networkd and its associated
  units.
- Repeated install preserved byte-identical original `network.json`, did not
  reapply the override or recapture enabled/active as original, and reran the
  bounded attach smoke successfully.

## Uninstall behavior verified

With a real product private project running, `network uninstall --yes` refused
before mutation because the lease was active. The bridge, taps, helper, files,
and running project remained intact. After public stop released the lease:

- dry-run emitted the exact remove/restore plan;
- apply acquired and held the host-global lease flock, then rechecked lease,
  bridge members, file hashes, helper/override, and manifest;
- it deleted `piglet0` before removing the NM unmanaged rule;
- restored helper root:root 0755 and all four networkd units to
  disabled/inactive;
- removed state last and used only exact unlink plus `rmdir` for
  manifest-owned empty paths.

Final checks found no `piglet0`, route, QEMU, tap, Piglet config, dpkg override,
lease/state/lock, `/etc/qemu`, `/var/lib/piglet`, or `/run/piglet` residue.
NetworkManager remained active. Project disks/state/keys under the isolated
E2E data root were retained.

## Remaining boundary

FR-110's Linux public install/status/repeated-install/active-lease-guard/
uninstall core is now native-passed. Darwin public install/uninstall, Linux
reboot persistence, RPM-family native evidence, partial transaction recovery,
and M2 soak remain.
