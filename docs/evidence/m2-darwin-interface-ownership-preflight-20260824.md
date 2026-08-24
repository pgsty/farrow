# Darwin interface ownership and foreign-subnet preflight — 2026-08-24

Result class: **native privileged migration + read-only product preflight
PASS** on macOS arm64. This closes a false-ownership hole where any foreign
interface carrying the expected `.1/24`—for example VirtualBox `vboxnet0`—could
previously be mistaken for Piglet's socket_vmnet interface.

## Failure reproduced before migration

The installed host-mode daemon was healthy but came from the earlier state
format, which had no observed BSD-interface identity. The new current binary
therefore refused to infer ownership from `bridge100`, the address, or the
listening socket:

```text
installation.status = partial
problem = partial Darwin Piglet network installation: 7/9 required public paths
interface.overlap = bridge100 / 10.10.10.1/24
route.overlap = 10.10.10.0/24 on bridge100
ready = false
exit_code = 7
```

The JSON SHA-256 was
`88637b54529f848ef9e4472af96d5b154628aa176e3203bc35027c6fc182a64a`.
This is the required fail-closed behavior: a plausible interface name and
address are not ownership evidence.

Before mutation, the host had no Piglet QEMU process, no active private lease,
no registered/running VirtualBox VM, no VirtualBox host-only interface, and no
VirtualBox NAT network. The exact protected old state named host mode,
`10.10.10.0/24`, pinned socket_vmnet 1.2.2, archive digest
`c7bf62308fbcfdc29bdfb8373c9b1951f7ac2396446e4390919796a94972e6dc`,
and persistent UUID `89e9f9e5-60cb-48a0-a739-b7fa6e49cde6`.

## Scoped migration

Because the old development binary was the only implementation that could
verify its pre-marker ownership format, the retained signed-source snapshot
binary first produced an exact uninstall plan, then removed only its listed
Piglet files/directories. It preserved `/opt/piglet/libexec/piglet-hosts-helper`
and the shared `/opt/piglet` parents. Plan and apply JSON hashes were:

```text
plan   5db099e67ecd88b1c18846a89b4d095c4c1d1fbd93f08dd92c7953d2f185754f
apply  ad92b401048ac499ccfb61393dc74aa749120c76d4ada090791766244f9aadf7
```

With OrbStack stopped, `com.apple.NetworkSharing` reached active count zero.
The current preflight then reported the default subnet eligible with only the
expected `installation.absent` warning (JSON hash
`0869306ed147247bb2e5c79d10c0cbfdd8b061be5d5af03e7ec4cddccfed3e9b`).

The new executor dry-planned and applied the same pinned archive, UUID, host
mode, and default `/24`. It captured exact `.1/24` interfaces immediately
before launch, waited for the Unix socket, required exactly one newly created
matching interface, and observed `bridge100`. The apply report states:

```text
interface = bridge100 newly created with exact 10.10.10.1/24
launchd = running
socket = root-owned and accepting
state = exact pinned plan and byte-identical public/protected interface identity
```

Plan/apply JSON hashes were
`99c21b569c51b6bf84d75e26cbab5742f6f9fcdcec547ea4faef098190b0f79e`
and
`6b0ba67de485bdd5605292c2b4c1fb3ec6ac99916f2c384214d05a74b1e0c720`.

## Final ownership proof

The root-owned non-secret marker is:

```text
/Library/Application Support/io.pgsty.piglet/network-interface.json
root:wheel 0644
```

Its root-only byte-identical twin is:

```text
/private/var/db/piglet/network-interface.json
root:wheel 0600
```

Both have SHA-256
`0b211dd43d689816caaa98acd7e01be802234760f112db67092ea2003b8333c0`
and bind the persistent vmnet UUID, CIDR, host address, and BSD name. The final
unprivileged product preflight returned `protected`, `healthy=true`, interface
`bridge100`, no findings, and exit 0; JSON SHA-256:
`314070c64a03cf5c37bbdc492f0a8e1abf4707662ca0947cd1a068750f373f99`.

The daemon is root while QEMU remains a user process boundary. Physical `en0`
remained `192.168.0.11`, the default gateway remained `192.168.0.1`, and no
system interface or route was deleted. Unit/race tests include a foreign exact
`vboxnet0` fixture: it is never adopted and remains an exit-6 resource
conflict even when the real marker-bound interface is also present.
