# M0-B macOS private native E2E — 2026-08-23

Result class: native real E2E. The pinned host-mode `socket_vmnet` spike passed
after one retained, diagnosed failure. This is not the product-level
`piglet network install/uninstall` or private-controller E2E.

## Execution identity

- Repository identity: unborn `main` branch; no commit existed, so no commit is
  claimed. The exact source/worktree snapshot is retained locally.
- Host: macOS 26.5.2, arm64, QEMU/qemu-img 11.1.0, HVF.
- Successful-run harness SHA-256:
  `db24d7db16b9ba0107751e94eb23bd7f6eb87e9983e2bf43e9c05e1746eff81d`.
- Image: `/Users/vonng/Library/Caches/piglet/product-e2e/private-m0-data-o81aSn/cache/images/sha256/aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476.qcow2`.
- Image SHA-256:
  `aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476`.
- Image was imported from the local u24 arm64 qcow2 into a mode-0444 digest
  cache; the source image was not modified.

The native command was:

```text
bin/piglet-private-m0 \
  --image <mode-0444 digest cache path> \
  --sha256 aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476 \
  --work-dir <new mode-0700 artifact directory> \
  --ready-timeout 300s \
  --restart-daemon=true
```

## Privileged boundary installed

The handoff plan was re-preflighted immediately before installation. All
targets were absent. Only fixed system tools installed the verified bytes:

| Target | Owner/mode | SHA-256 or fact |
|---|---|---|
| `/opt/piglet/libexec/socket_vmnet` | root:wheel 0755 | `b8a72a62237312f2f756027dea504a844edeb40014702d4a320292c026d282b0` |
| `/opt/piglet/libexec/socket_vmnet_client` | root:wheel 0755 | `2d2e364b808f0b43a92bdd7ac3c7b390d6b55a33c761c61c0738d688286a3eff` |
| `/Library/LaunchDaemons/io.pgsty.piglet.vmnet.plist` | root:wheel 0644 | `c451e59080ab328da20f62de470f5199423e562bbd44b4681f4c8c3e4d754660` |
| `/private/var/db/piglet/network.json` | root:wheel 0600 below mode-0700 directory | `76dbf7a980a363d5cea545b60f26a1ea703f015f6c6bc3c38632f58f13db12c3` |
| `/private/var/run/piglet` | root:wheel 1777 | host-global lease root |
| `/private/var/run/piglet-vmnet.sock` | root:staff 0770 | daemon-created stream socket |

The archive SHA-256 was
`c7bf62308fbcfdc29bdfb8373c9b1951f7ac2396446e4390919796a94972e6dc`.
The persistent interface UUID was
`89e9f9e5-60cb-48a0-a739-b7fa6e49cde6`. Launchd reported the root daemon
running in host mode with gateway `10.10.10.1`, DHCP end `10.10.10.8`, `/24`,
and no isolation flag. QEMU processes remained UID 501.

## Retained failure and repair

The first run (`2026-08-23T15:24:11Z` to `15:24:56Z`, project
`00f5c090-84c6-441c-be96-cc71c0c8818c`) passed host address, host-to-VM,
VM-to-VM, route, internet, and private no-default/no-DNS checks, then failed
control lateral SSH because `/home/dba/.ssh/config` was absent. The serial log
showed `cc_write_files` failed: cloud-init `write_files` ran before
`users-groups`, so owner `dba:dba` did not yet exist.

- Evidence:
  `/Users/vonng/Library/Caches/piglet/product-e2e/private-m0-artifacts-txQ8TQ/evidence.json`
- Evidence SHA-256:
  `96ee0c5979996ec88664755db3bbe263bb39e36528a2df6c1f23ce75a62b4e37`.

The fix stages the control key/config as root:root 0600, validates the Ed25519
key pair, installs it after the account exists, deletes the staging sources,
and runs the exact private interface/address/route/DNS contract before writing
the ready marker. The finalizer is fail-closed. A later Linux failure also
proved the new deferred QMP cleanup removes QEMU, tap, QMP, pidfile, and runtime
directories. The two empty runtime directories left by this earlier harness
version were subsequently verified as owned, mode-0700, empty, and removed
with exact `rmdir` calls.

## Passing run

The second run (`2026-08-23T15:26:33Z` to `15:27:35Z`, project
`580f92d2-38e4-462f-9b8f-148a57a5fbc9`) passed:

- two real arm64/HVF VMs at `10.10.10.10` and `10.10.10.11`;
- QEMU UID 501 with management user NAT plus private stream NIC;
- host-to-both-VM direct private SSH;
- VM-to-VM route and ping over `private0`;
- management default route/DNS and HTTP 200 internet on both VMs;
- exact private address, interface UP, no private default route, and no DNS;
- control-only private key and lateral SSH from `meta` to `node-1`;
- launchd daemon restart, UUID reuse, and QEMU stream reconnect;
- QMP-verified shutdown and no QEMU/QMP/pid/runtime residue.

- Evidence:
  `/Users/vonng/Library/Caches/piglet/product-e2e/private-m0-artifacts-fixed-NRWmX1/evidence.json`
- Evidence SHA-256:
  `9c614ea0613e63e55e9b2150227912ac7b9444954ecc5c44322af7a067b90a08`.

Artifact directories contain generated project keys and control seed media.
They remain mode-0700/0600 sensitive evidence and must not be bundled or
published.

## Remaining boundary

Host-mode M0-B is passed. Shared-mode equivalence, product
install/repeated-install/status/uninstall, active-lease uninstall refusal, Go
FD fallback in the composed product lifecycle, reboot, and product private CLI
remain in progress. Ordinary-user status intentionally warns that the
mode-0700 `network.json` cannot be fully inspected; daemon/plist/socket/route
are verified.
