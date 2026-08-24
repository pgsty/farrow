# ADR-0004: socket_vmnet v1.2.2 pin, DHCP boundary, and runtime chain

- Status: accepted for native host and shared modes; subnet availability is a hard gate
- Date: 2026-08-23

## Supply-chain facts

The macOS private spike pins the immutable upstream v1.2.2 release:

| Arch | Archive SHA-256 |
|---|---|
| arm64 | `c7bf62308fbcfdc29bdfb8373c9b1951f7ac2396446e4390919796a94972e6dc` |
| x86_64 | `2968a82c97e692c2d36f87230152e8018e00589c1b598e8257775adfe83800a1` |

The hashes are embedded in Piglet and checked before tar parsing. Upstream's
same-release `SHA256SUMS` is not the runtime trust root. On the current arm64
host, GitHub artifact attestation verification also succeeded for the exact
archive digest, release workflow
`.github/workflows/release.yaml@refs/tags/v1.2.2`, SLSA provenance predicate,
and Rekor timestamp.

The arm64 executable is a thin arm64 Mach-O linked only to vmnet.framework and
libSystem. Its embedded signature is ad-hoc/linker-signed, has no team ID, and
`spctl` rejects it. It is therefore not notarized. The curl-downloaded archive
and extracted binary had `com.apple.provenance`, but no
`com.apple.quarantine` xattr. Removing quarantine is neither needed nor a claim
of notarization.

## Network-mode decision

Prefer host mode, with shared as an equivalent fallback only after the selected
global subnet passes a real vmnet startup/host-address check:

```text
gateway 10.10.10.1
mask 255.255.255.0
DHCP end 10.10.10.8
persistent --vmnet-interface-id UUID
```

Do not pass `--vmnet-network-identifier`. Upstream v1.2.2 documents that flag
as creating an isolated DHCP-less host network, while Piglet explicitly
forbids isolation. Disabling DHCP is therefore not used as a correctness
dependency; the `.8` boundary leaves `.9-.254` for static addresses.

The first shared product run failed, but a later audit found stale neighbor
MACs and no contemporaneous proof that VirtualBox/other vmnet consumers or the
whole `/24` were absent. A controlled rerun found `VMNET_SHARING_SERVICE_BUSY
(1009)` on `10.10.10.0/24`. With the same binary and OrbStack still running,
host mode immediately started on `172.31.250.0/24` and shared mode immediately
started on `172.31.251.0/24`.

After the private guest renderer disabled IPv6 RA/link-local configuration, a
real two-node shared E2E on the alternate subnet passed host→VM, VM→VM ICMP and
SSH, private no-DNS/default-route, and management egress. The categorical
shared rejection is therefore withdrawn. See
[`m0-b-darwin-shared-clean-subnet-20260824.md`](../evidence/m0-b-darwin-shared-clean-subnet-20260824.md).

The installer must not treat a listening socket as readiness. It also requires
the configured host address and reports vmnet 1009 as an external sharing
service/subnet conflict. Piglet never silently substitutes a different subnet;
all guest addresses and the one host-global network must move together.

The implemented escape hatch accepts one explicit canonical RFC1918 IPv4 `/24`
for the whole host. It derives host `.1`, DHCP end `.8`, and static `.9-.254`;
non-default use always warns. Standalone/default typed preflight checks routes,
interfaces, requested addresses, installed-network equality, readiness, and
ownership before mutation. Embedded profile rebasing preserves node suffixes;
user YAML is never silently rewritten. Native public custom install,
two-node shared traffic, daemon reconnect, uninstall, and original-default
rollback passed; see
[`m2-network-preflight-custom-subnet-20260824.md`](../evidence/m2-network-preflight-custom-subnet-20260824.md).

## QEMU runtime decision

For QEMU 11.1, the preferred backend is:

```text
-netdev stream,id=private,server=off,
  addr.type=unix,addr.path=<socket>,reconnect-ms=1000
```

`reconnect-ms` is the current spelling; QEMU removed the old `reconnect` in
10.2. A native real-QEMU test closed the Unix peer and observed an automatic
second connection. The fallback dials the daemon in Go, passes the connected
file through `ExtraFiles`, and uses `-netdev socket,id=private,fd=3`; that path
also completed a real QMP lifecycle. `socket_vmnet_client` remains diagnostic
only.

The privileged host-mode follow-up installed the exact pinned bytes and booted
two UID-501 arm64/HVF VMs. It verified host address `.1`, DHCP boundary `.8`,
host→VM, VM→VM, management internet, control lateral SSH, persistent UUID, and
stream reconnect after launchd daemon restart. See
[`m0-b-darwin-private-native-20260823.md`](../evidence/m0-b-darwin-private-native-20260823.md).

This accepts host and shared modes, the preferred runtime chain, public
install/uninstall, and the composed Go-FD fallback. Host remains the default;
shared is valid only on a verified non-conflicting subnet with the same guest
contract.
