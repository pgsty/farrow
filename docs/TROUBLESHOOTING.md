# Troubleshooting

## `qemu-system-*` or `qemu-img` is missing

On the current macOS development host the intended command is:

```bash
brew install qemu
```

The first M0 attempt on 2026-08-23 stalled fetching Homebrew signed package
metadata. A temporary USTC API/bottle domain completed the normal Homebrew
install; QEMU 11.1.0 is now present. Do not infer that another host has QEMU or
HVF from this local result—run `piglet doctor` and the native smoke there.

## Go module download times out

The current shell timed out contacting `proxy.golang.org` and direct GitHub.
The dependency was recovered with a one-command TUNA Go proxy override and is
now pinned in `go.sum`. Do not vendor an ad-hoc ISO writer as a workaround.

## QMP socket is absent

Distinguish a stopped/crashed VM from a stale runtime directory. `stop` may use
SIGTERM and finally SIGKILL only when QMP is proven unavailable and executable,
process start time, argv hash, and invocation all still match state; identity
mismatch never signals. If that fail-closed path refuses, run
`piglet repair --dry-run` first. Use `piglet repair --force` only after reviewing
its exact scoped actions; do not manually delete disks.

If prepare reports an unsafe or overlong runtime path, inspect
`XDG_RUNTIME_DIR`. It must be a canonical absolute directory owned by the
invoking UID with mode 0700. Unset it to use Piglet's UID-isolated short
fallback; do not point it at `/tmp`, home, or a shared directory.

## A quick port is occupied

Piglet probes only the documented finite candidates. The actual selected port
must appear in the resolved spec and status. If all candidates are occupied,
free a port or add an explicit `--forward`; Piglet must not retry forever.

## Inspect lifecycle events

Use `piglet logs --source events` for the structured operation timeline and
`piglet logs --source events --follow` while reproducing a problem. Events
include operation UUID, action, phase, level, and a redacted message; SSH
command arguments are deliberately omitted.

## Drift requires restart

Run `piglet plan` first. CPU, memory, and forward changes on a running VM return
exit 4 and do nothing unless `piglet up --restart ...` is explicit. Stopped
safe drift applies on `up`. Recreate/destructive classes never cross that flag.
If a reconcile journal remains after a host/CLI crash, inspect it with
`piglet repair --dry-run`; do not edit state or resize disks manually.

Private projects conservatively classify any desired spec-hash change as
destructive recreate. `up -f` returns exit 4; review `plan`, then use only
`recreate -f <config> --force`. The complete desired preflight and persistent
disk compatibility checks run before stop/destroy.

## Private subnet or VPN conflict

Run `piglet network preflight --json` or
`piglet network preflight -f piglet.yaml` before installation. The same typed
gate runs automatically before install and private plan/up/start. A conflicting
route/interface/address is an error requiring an explicit global-network
decision; private mode must not silently use slirp instead.

If `10.10.10.9`–`.254` already accepts SSH while no Piglet lease is active,
`plan`/`up` fail before creating state and `doctor` names the address. Stop the
conflicting VirtualBox/other VM runtime or make an explicit global addressing
decision; do not bypass the collision probe.

## socket_vmnet socket exists but host `.1` is absent

A Unix socket can be created before vmnet.framework successfully creates its
interface. If doctor reports no `10.10.10.1`, inspect
`/var/log/piglet-vmnet/stderr.log`. Error 1009 is
`VMNET_SHARING_SERVICE_BUSY`: another virtualization/sharing service conflicts
with the requested subnet. Stop the conflicting VM/service or make one explicit
global subnet change; changing only guest addresses or silently using slirp is
not valid.

To move the complete lab instead:

```bash
piglet init full --network-cidr 172.31.251.0/24 >piglet.yaml
piglet network preflight -f piglet.yaml
piglet network uninstall              # dry plan, then --yes
piglet network install --cidr 172.31.251.0/24 ...
```

Non-default networks are restricted to canonical RFC1918 IPv4 `/24` ranges and
always warn. Do not alter only the daemon, node IPs, or lease file.

Host is the default mode, but shared is not categorically refused. A native
two-node shared run passed on a conflict-free subnet after the private guest NIC
disabled IPv6 RA/link-local DNS.

## Historical Rocky/EL8 arm64 EFI-stub failure

The exact Rocky 8.10 arm64 formal image uses a fixed 64 KiB-granule kernel. On
the tested Apple arm64 HVF host it stops with `This 64 KB granular kernel is not
supported by your CPU` before cloud-init/SSH. This is not a timeout to retry.
The owner removed EL8 from the v1 target, so `el8`/`rocky8` are no longer
embedded aliases. Do not use TCG or a foreign CPU model to reintroduce it as a
native pass; the old evidence remains only as a compatibility record.

## Destructive recovery

Do not use a global `nuke`. Use scoped status, repair dry-run, node destroy, and
network uninstall. If ownership or process identity cannot be proved, preserve
the resource and report the exact manual evidence needed.

Ordinary destroy preserves project keys and every `persistent: true` data disk.
Compatible `up`/recreate reattaches the marker-owned retained disk. Only
`destroy --force --delete-persistent` supplies the separate confirmation that
deletes a fully validated persistent store.

Only after all node and persistent-disk artifacts are gone, preview
`piglet project purge-keys --dry-run`; use `--yes` to delete the exact
three-file allowlist (`id_ed25519`, public key, `known_hosts`). Any unexpected
entry, unsafe owner/mode/link, live process, node artifact, or retained disk
blocks the whole operation before the first deletion.
