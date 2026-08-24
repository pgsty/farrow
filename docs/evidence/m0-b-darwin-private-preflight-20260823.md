# M0-B macOS private preflight — 2026-08-23

Historical preflight result: supply-chain, plan, and QEMU runtime-chain checks
passed. The formerly blocked privileged host-mode E2E was later executed and
passed; see
[`m0-b-darwin-private-native-20260823.md`](m0-b-darwin-private-native-20260823.md).
The absent-target/blocker statements below describe this earlier snapshot.

## Artifact and attestation

- Release: upstream immutable socket_vmnet v1.2.2
- Archive: `socket_vmnet-1.2.2-arm64.tar.gz`, 21,254 bytes
- Embedded/archive SHA-256:
  `c7bf62308fbcfdc29bdfb8373c9b1951f7ac2396446e4390919796a94972e6dc`
- Extracted daemon SHA-256:
  `b8a72a62237312f2f756027dea504a844edeb40014702d4a320292c026d282b0`
- Extracted client SHA-256:
  `2d2e364b808f0b43a92bdd7ac3c7b390d6b55a33c761c61c0738d688286a3eff`
- GitHub/Sigstore attestation: passed, subject digest matched, SLSA v1
  predicate, OIDC issuer `https://token.actions.githubusercontent.com`, signer
  SAN `https://github.com/lima-vm/socket_vmnet/.github/workflows/release.yaml@refs/tags/v1.2.2`, Rekor timestamp present
- Binary: thin arm64 Mach-O, ad-hoc linker signature, no team ID, valid
  designated requirement, `spctl` rejected; not notarized
- xattrs: `com.apple.provenance` present; no quarantine xattr observed

## Safe extraction and plan

The Go verifier rejects archive digest mismatch, traversal, non-regular special
entries, oversized archives/entries, duplicates, symlinks, and missing
executables. Only two binaries are extracted into an empty mode-0700 staging
directory and each is re-hashed.

Current staged plan:

```text
/Users/vonng/Library/Caches/piglet/socket_vmnet/v1.2.2/stage-I9in07
interface ID: 89e9f9e5-60cb-48a0-a739-b7fa6e49cde6
```

`plutil -lint` passed. Planned targets remain absent:

```text
/opt/piglet/libexec/socket_vmnet                 root:wheel 0755
/opt/piglet/libexec/socket_vmnet_client          root:wheel 0755 diagnostic only
/Library/LaunchDaemons/io.pgsty.piglet.vmnet.plist root:wheel 0644
/private/var/db/piglet/network.json               root:wheel 0600
/var/log/piglet-vmnet/                            root:wheel 0755
/private/var/run/piglet-vmnet.sock                daemon-created, group staff
/private/var/run/piglet-vmnet.pid                 daemon-created
```

The daemon plan is host mode, gateway `.1`, DHCP end `.8`, `/24`, persistent
interface ID, staff socket group, and no isolation/network identifier.

## Real QEMU connection probes

On QEMU 11.1.0 with HVF initialization:

- Unix `stream` + `reconnect-ms=100` connected to a fake framed peer, the peer
  closed, and QEMU reconnected within the deadline: passed.
- Go `DialUnix` + `ExtraFiles` + `socket,fd=3` completed a real QMP identity and
  quit lifecycle: passed.

These are native integration tests, not private-network E2E.

## Historical blocker (closed)

At this snapshot `sudo -n true` reported `a password is required`; no host
networking had changed. The later native run installed the staged bytes and
verified host address, `.8` DHCP boundary, daemon UUID reuse/restart,
host-to-VM, VM-to-VM, QEMU UID, and stream reconnect. Shared fallback remains
not run.
