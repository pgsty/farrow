# Darwin product network executor — 2026-08-24

Result class: native real product install/repeated-install/active-lease-guard/
uninstall/reinstall E2E on macOS arm64.

## Execution identity

- Host: macOS 26.5.2 arm64.
- Product binary SHA-256:
  `77a701d2a4cafa66ffaaac89f9195c253f2ca99768e2c2a0240fd6251b24c651`.
- Repository: unborn `main`; no commit is claimed.
- Pinned archive:
  `/Users/vonng/Library/Caches/piglet/socket_vmnet/v1.2.2/socket_vmnet-1.2.2-arm64.tar.gz`.
- Archive SHA-256:
  `c7bf62308fbcfdc29bdfb8373c9b1951f7ac2396446e4390919796a94972e6dc`.
- Persistent interface ID:
  `89e9f9e5-60cb-48a0-a739-b7fa6e49cde6`.

## Existing-install adoption refusal and no-op verification

The executor first read the root-only state through a fixed privileged
`/bin/cat`, decoded it with unknown/trailing-field rejection, regenerated the
entire pinned plan, and compared exact plist/state bytes, binary hashes,
owners, modes, directories, and lease boundary. Both dry-run and `--yes`
returned action `none`; no installed path was overwritten or daemon restarted.

## Public uninstall

With the product project stopped and no active lease:

```text
piglet network uninstall --json
piglet network uninstall --yes --json
```

Dry-run listed only the pinned daemon/client/plist/state, daemon-created
socket/pid, two known log files, shared lease lock, and exact owned directories.
Preflight rejected unexpected directory entries and verified log/lock metadata,
binary/state/plist identity, absence of QEMU socket users, and lease status.

Apply held the host-global lease flock, rechecked the plan, booted out only
`system/io.pgsty.piglet.vmnet`, waited for the socket to disappear, unlinked
the exact files, and used only `rmdir` on proven-empty directories. Postchecks
found no service, `.1` interface, socket/pid, daemon/client/plist/state/log/
lock, `/opt/piglet`, state/log directory, QEMU, or active lease residue.
Stopped project qcow2/state/keys were retained.

## Public fresh reinstall

Fresh dry-run and apply used:

```text
piglet network install \
  --archive <pinned-v1.2.2-arm64.tar.gz> \
  --interface-id 89e9f9e5-60cb-48a0-a739-b7fa6e49cde6 \
  [--yes] --json
```

The user-mode verifier checked the embedded archive digest and safe tar
structure, extracted only the two individually pinned binaries into a new
mode-0700 staging directory, and generated exact plist/state/empty root-owned
0666 lease lock. `--yes` invoked only fixed sudo system argv to install the
root:wheel paths and bootstrap/enable/kickstart the one launchd label.

Post-install verification regenerated and compared state/plist bytes and
binary hashes, confirmed root ownership/modes, and required the Unix stream
socket to accept a connection. Launchd is currently running with the same
interface ID and no active product lease.

The retained public two-node project then completed `start`, direct private
contract/data-canary checks, and `stop`, proving the root-owned lock works with
ordinary lease flock without chmod. A second running-project attempt to
`network uninstall --yes` was refused before mutation; the project remained
healthy and stopped normally.

## Remaining boundary

FR-100 public host-mode plan/install/repeat/status/active-lease guard/uninstall/
reinstall now passes. A later alternate-subnet run also proved shared-mode
fallback equivalence and corrected socket-only readiness. Remaining work is
automatic pinned archive acquisition, host reboot and transaction crash
injection, Intel Mac native evidence, and soak.
