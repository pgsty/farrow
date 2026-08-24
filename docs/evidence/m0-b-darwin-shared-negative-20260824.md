# socket_vmnet shared-mode negative result — Darwin arm64 — 2026-08-24

> Historical/confounded evidence: later audit found stale neighbor MACs from
> stopped projects and no contemporaneous VirtualBox/vmnet-consumer snapshot.
> A clean alternate-subnet run subsequently passed shared mode after disabling
> IPv6 RA on the private NIC. Do not use this file to claim that shared mode is
> categorically broken; see
> [`m0-b-darwin-shared-clean-subnet-20260824.md`](m0-b-darwin-shared-clean-subnet-20260824.md).

Result class: **native real failed E2E retained as a product gate**. The test
temporarily replaced the verified host-mode service with an equally pinned
shared-mode service, using the same v1.2.2 binaries, gateway
`10.10.10.1`, DHCP end `.8`, mask, socket path and persistent interface UUID
`89e9f9e5-60cb-48a0-a739-b7fa6e49cde6`.

## Procedure

1. Stop every Piglet private project and prove the lease absent.
2. Run public network uninstall and verify removal.
3. Run the exact public install dry plan and apply with
   `--vmnet-mode=shared`; installed state recorded `mode: shared` and exact
   pinned hashes/metadata.
4. Create the ordinary two-node product private configuration with static
   `.10/.11`, dual NICs and management NAT.
5. Test host→VM, VM→VM, VM→internet and the normal private route/DNS contract.
6. Stop/destroy the scoped project, uninstall shared mode, reinstall host mode
   with the original UUID, and reinstall the separately root-owned hosts
   helper.

## Failure facts

The first attempt exposed an overly strong guest-side ARP refresh check: shared
mode does not answer guest→host ICMP, so `ping 10.10.10.1` failed cloud-final.
That failure is retained in
`/Users/vonng/Library/Caches/piglet/shared-product-darwin-oE7yYP/failed-shared-icmp-debug.tar.gz`,
SHA-256 `8910154c335afee119848cb3c70594b1b26a27cf9a8281692fa260f445c3d054`.
The readiness contract was corrected to emit a Bash UDP datagram solely to
refresh the neighbor table; it no longer treats host ICMP as a product
requirement or depends on a guest `ping` package.

With corrected seeds, both nodes reached their ready markers. Each had the
right static IP, private route, no private DNS/default route, and successful
HTTP egress through management NAT. However the required shared-network data
plane still failed:

- host ICMP to both `.10` and `.11`: 100% loss;
- control-node SSH to `.11`: `No route to host`;
- therefore VM→VM fixed-IP TCP failed;
- the run did not establish that the whole `/24`, `.11`, or the Apple sharing
  service was free of competing consumers.

The second redacted bundle is
`failed-shared-reachability-debug.tar.gz`, SHA-256
`a0edecf9d54a16dbc9f2be4f5970b4e28bb97bea87711e4772908e51f6e52b20`.
It preserves the successful ready/state/route/egress facts and the failed
reachability environment without keys, seeds or disks.

## Decision and restoration

At the time, this run was used to reject shared mode. That causal conclusion is
superseded: the later clean alternate-subnet E2E passed the complete fixed-IP
contract. The durable fact from this file is only that this particular,
contaminated default-subnet run failed.

The test performed public stop/destroy, shared network uninstall, and exact
host-mode reinstall. The restored state has the original pinned release,
`mode: host`, `10.10.10.1/.8`, and the same UUID. The root-owned hosts helper
was reinstalled with the companion binary digest. No shared project QEMU,
runtime directory or lease remains.

An unrelated running VirtualBox/Vagrant guest was observed later, but its exact
identity and lifetime were not captured during this run. The later audit also
found stale `.10-.13` neighbor entries from stopped Piglet projects. These are
material coexistence/confounding facts, not separable proof against shared.
