# Private desired-state drift and explicit recreate — Darwin arm64 — 2026-08-24

Result class: **native product drift/recreate E2E**. A running one-node private
project created from a 1-vCPU strict config was compared with a 2-vCPU desired
config and changed only through explicit destructive recreate.

- Window: `2026-08-24T07:36:21Z`–`07:37:19Z`.
- Mode-0700 evidence root:
  `/Users/vonng/Library/Caches/piglet/private-drift-darwin-go127-20260824-01`.
- Piglet SHA-256:
  `1d348c52e3fa0fd6981a3658a43070c3da408f6182cee8ec17c491e0067c1f32`.
- Project `af68ea5f-ff52-4609-8479-291fc46087f0`.
- Evidence checksum-list SHA-256:
  `aad8956a39471094f314c219452a7717469e96cb6a03b1742b8446332887dde2`.

The initial guest proved one vCPU. With the 2-vCPU desired file:

```json
{
  "action": "recreate",
  "destructive": true,
  "nodes": ["meta"],
  "lease_active": true
}
```

Ordinary `up -f` returned stable state-conflict exit 4 and
`error=recreate_required`; it did not silently restart or mutate state.
`recreate --force -f` first validated the complete desired network and
persistent-disk compatibility, then performed stop→destroy→up. The replacement
guest proved two vCPUs and retained the same fixed IP contract.

Final stop left the 64 GiB root and 128 GiB data qcows clean. Full destroy
returned the node absent and final network status showed a healthy backend with
inactive lease.

This deliberately uses conservative recreate for private hash drift. Quick has
finer stopped-only reconciliation; a future private in-place reconcile may
optimize safe CPU/memory/forward/seed changes, but current behavior is complete,
explicit, and fail-closed rather than pretending such optimization exists.
