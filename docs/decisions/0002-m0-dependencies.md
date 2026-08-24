# ADR-0002: M0 dependency baseline

- Status: accepted and pinned
- Date: 2026-08-23

## Decision

Use the Go standard library for command execution, JSON, QMP, hashing, process
control, and the initial M0 CLI. Use `github.com/diskfs/go-diskfs` v1.9.4 only
for ISO9660 creation/reading.

The library is MIT-licensed, tagged 2026-07-19, requires Go 1.25, and includes
ISO/Joliet fixes in its recent tag history. The current host has Go 1.26.7.
It is used because the PRD forbids both hand-written unverified ISO code and
runtime dependencies on `genisoimage`, `xorriso`, or `cloud-localds`.

## Dependency evidence

On 2026-08-23 `proxy.golang.org` and direct GitHub access timed out. The TUNA
Go proxy returned the complete upstream tag list, and v1.9.4 was pinned through
the Go checksum/module workflow. Later M1 slices added the small pinned
dependencies recorded in `go.mod`: strict YAML, minisign-compatible signature
verification, and `x/crypto/ssh` for parsing and matching the control Ed25519
key pair. CIDATA is round-tripped with the library reader in native
unit/integration tests. `x/crypto` is v0.52.0 after the reachable
GO-2026-5018 finding described in the vulnerability evidence.

## Rejected alternatives

- Importing Lima: far larger and outside the selected architecture.
- Shelling out to host ISO tools: violates the guest-seed contract.
- Hand-writing ISO9660: insufficiently mature/verified for a boot contract.
