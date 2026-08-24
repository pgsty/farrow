# Network preflight hardening and current native validation — 2026-08-24

Result class: **current-code unit/race plus native Darwin and isolated-native
Linux validation**. This closes gaps found by an independent final audit of the
initial typed preflight/custom-subnet implementation.

## Corrected behavior

- Darwin now parses the complete IPv4 `netstat -rn -f inet` table rather than
  point-querying `.1` and `.9`; broad and narrow routes are visible while
  cloned ARP neighbor entries are excluded.
- Linux recognizes `blackhole`, `unreachable`, `prohibit`, `throw`, `local` and
  other typed routes instead of treating the first token as a prefix.
- An installed backend owns only its exact host `.1/24` address, exact connected
  `/24` route, and host-local `.1/32`; a broader/narrower route or extra address
  on the same interface is still a resource conflict.
- Exact installed network without its owned `/24` route is capability exit 3.
- Darwin verifies pinned plist bytes plus daemon/client digests; Linux verifies
  public unit/marker bytes, helper ownership/package policy, and readable
  manifest file digests.
- Root-only state that an ordinary user cannot traverse is reported as
  `protected`, not falsely `exact`; public projection, runtime, interface and
  route still must all pass.
- A missing Darwin runtime socket is capability/not-ready, not installation
  integrity corruption. Healthy protected installs suppress stale 1009 logs.
- Current or observed `VMNET_SHARING_SERVICE_BUSY (1009)` is typed resource
  exit 6.
- Debian lifecycle requires the package-owned root:kvm 4750 helper and exact
  reversible dpkg policy. RPM lifecycle now accepts and verifies the
  package-owned root:root 4755 policy instead of applying Debian assumptions.
- `restart`/`recreate` regression tests prove preflight failure occurs before
  stop/destroy mutation.
- Invalid `init --network-cidr` returns usage exit 2.
- `doctor` and `network status` understand both `exact` and `protected`.

## Darwin native results

Current Go 1.27 binary SHA-256 at the first native run:
`5d71dc175e97336810569d8019b5b9e53dd8bbdbca0b9c456aa570c5da9d32d6`.
After the optional-Linux fix the final Darwin full/Quick binary was
`d0e2e5475bc55b4bff6be5b637e0ab9d27342546d0f460798a0ba7bd2ac715ee`;
the Darwin preflight code was unchanged between them.

Exact current `profiles/minio.yaml` returned:

```text
installation.status=protected
mode=host cidr=10.10.10.0/24 interface=bridge100 healthy=true
findings=[] ready=true exit_code=0
```

A read-only probe of the physical `192.168.0.0/24` LAN returned resource exit
6 and independently identified:

- `en0` interface overlap;
- connected `/24` plus specific overlapping routes;
- active `.10` and `.11` TCP/22 addresses;
- installed/requested global-network mismatch;
- the mandatory non-default warning.

This demonstrates that a VirtualBox/other VM host-only interface or an active
node in the candidate range is no longer misdiagnosed as a generic socket_vmnet
failure. The remedy is to stop/remove the conflicting virtual network or choose
one explicit coordinated RFC1918 `/24`; Piglet never guesses a subnet.

## Linux isolated-native result

The current linux/amd64 binary ran with real `/dev/kvm` in an Ubuntu 24.04
systemd/Docker network namespace on `ai`. Initial clean preflight returned
`installation.absent` warning/ready/exit 0. The minimal namespace had no
NetworkManager package, exposing and fixing one additional bug: exact systemd
`not-found/inactive/dead` is valid for optional NetworkManager, while required
networkd units remain strict.

After rebuilding, public install and uninstall passed twice for the current
MinIO and full runs. Applied checks proved `piglet0 10.10.10.1/24`, non-root
QEMU helper attach, root:kvm 4750 override, protected/healthy runtime, and
inactive lease. Final cleanup proved:

- no `piglet0`, route, manifest, networkd/tmpfiles/bridge.conf or lease path;
- no dpkg override;
- helper restored root:root 0755;
- networkd prestate restored;
- physical `ai` host never received a Piglet route or file.

Evidence links:

- [`current Linux MinIO`](m2-private-minio-product-linux-amd64-dba-go127-isolated-20260824.md)
- [`current Linux full`](m2-private-full-product-linux-amd64-dba-go127-isolated-20260824.md)
- [`current Darwin MinIO`](m2-private-minio-product-darwin-arm64-dba-go127-20260824.md)
- [`current Darwin full`](m2-private-full-product-darwin-arm64-dba-go127-20260824.md)

## Automated gates

Targeted unit/race, full `go vet ./...`, full Staticcheck, and Linux amd64
CGO-disabled test compilation passed. The complete repository unit/race gate
also passed after making the Quick port-allocation test independent of unrelated
host listeners.

This evidence does not replace a bare-host RPM-family lifecycle, host reboot,
or current Linux custom-subnet privileged apply on a disposable physical host.
