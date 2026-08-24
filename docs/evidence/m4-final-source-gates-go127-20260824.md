# Frozen source gates — Go 1.27 — 2026-08-24

Result class: **current complete source gate** after network preflight,
Piglet-only/all-`dba`, persistent disks, key purge, `--no-wait`, signal
fallback, selected private lifecycle, and explicit private recreate changes.

Final local development binary identities before release-tool rebuilding:

```text
piglet               1d348c52e3fa0fd6981a3658a43070c3da408f6182cee8ec17c491e0067c1f32
piglet-hosts-helper  4d218aef0f83baf20a9bd78d8e2bc76b1bae084cb0371ab22be6d7f6be6f8942
Go                   1.27.0
```

The repository has no publishable commit/tag, so these are exact byte
identities, not an invented revision.

## Complete gate

The final command:

```bash
make check
```

passed all of:

- `go test ./...`;
- `go test -race ./...`;
- `go vet ./...`;
- Staticcheck 2026.2;
- govulncheck 1.7.0: zero reachable vulnerabilities;
- Darwin/Linux × arm64/amd64 cross-builds;
- Pigsty wrapper tests;
- exact module/Go stdlib/YAML NOTICE license verification.

Additional release-facing checks passed:

```text
13 profiles / 85 nodes pinned source+provider topology plus all-dba parity
goreleaser check
actionlint .github/workflows/release.yml
shellcheck packaging/tests scripts
```

Targeted unit/race and native evidence cover:

- full-route typed preflight, protected ownership, 1009, RPM/Debian helper
  policy, and optional-absent NetworkManager;
- default/custom network and current Quick/full/MinIO on both Tier-1 VM
  semantics;
- persistent store preserve/reattach/recreate/explicit delete and key purge;
- QMP-unavailable SIGTERM/SIGKILL/PID-reuse safety plus native fault injection;
- selected private plan/up/start/stop/restart/status with lease preservation;
- typed private recreate-required conflict and explicit `recreate -f --force`.

The retained `dist/` from earlier in the day predates these changes and is not
part of this gate. Release artifacts must be rebuilt from a tagged synthetic
development snapshot (or a real release tag), pass strict two-run byte identity,
and be verified with the current archive/package/checksum/SBOM/wrapper/license
inventory before use.
