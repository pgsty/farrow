# Resolved config/runtime safety closure — Go 1.27 — 2026-08-24

Result class: **implementation + unit/integration PASS**. This closes fields
that strict YAML previously accepted but did not apply and removes Quick's
hard-coded login-user restriction. It is not a substitute for a fresh native
VM matrix or the final release rebuild.

## Runtime behavior

- `ssh.wait_timeout` is persisted in resolved JSON as nanoseconds and is used
  by both Quick and private readiness. Old resolved/state documents with no
  field retain the historical 180-second default; negative values fail before
  mutation. CLI operation deadlines expand with an explicitly longer wait
  instead of truncating it at the old fixed command timeout. A test-only
  manager override remains available for bounded E2E.
- `storage.data_root` now follows
  `PIGLET_DATA_HOME -> storage.data_root -> XDG_DATA_HOME/piglet -> platform
  fallback`. The actual selected root is materialized into resolved state.
- An existing `.piglet/project.json` remains authoritative. A new explicit
  environment/config root that differs from its marker returns typed
  `project data-root migration required` and CLI exit 4; Piglet does not create
  a second state tree or treat recreate as a data-root migration.
- Private image resolution receives the same configured root before project
  creation, so cache, project state, and disks cannot split across roots.
- New QMP/pidfile paths use a validated owner-0700 `XDG_RUNTIME_DIR` hierarchy,
  with a short UID-isolated platform fallback and socket-length gate. Empty
  managed parents are pruned; exact old flat `/tmp` paths remain startable.
- Quick accepts every username allowed by strict config validation. Cloud-init,
  readiness, `ssh`, `exec`, generated SSH config, human status, and JSON status
  use the resolved value. Empty legacy state falls back to `dba`; unsafe values
  fail closed.

## Safety visibility

- Explicit non-loopback TCP forwards emit a prominent warning during
  validate/plan/up while loopback forwards do not.
- Image info/pull and successful lifecycle use emit a warning when the active
  manifest entry is `testing` or `deprecated`; human image info includes the
  status explicitly.
- `destroy` and `recreate` accept a typed confirmation on a real TTY. Non-TTY
  callers still require `--force`; mismatched input aborts before lifecycle
  mutation.

## Verification

The focused commands passed on Darwin arm64 with Go 1.27.0:

```text
go test ./internal/project ./internal/config ./internal/spec
go test ./internal/quick ./internal/private ./cmd/piglet ./internal/vm
go test ./internal/profile
```

Coverage includes precedence and unsafe-root rejection, marker/root migration,
duration round-trip/default/negative handling, resolved custom-user validation,
readiness and OpenSSH argv propagation, legacy user fallback, warning policy,
TTY/non-TTY confirmation, and CLI exit-code mapping. The embedded profile YAML
bytes did not change; resolved contract hashes changed intentionally because
the 180-second timeout is now explicit instead of silently discarded.

Native VM evidence from earlier in the day remains valid for the guest/QEMU
contract but predates this final source shape. The final Go 1.27 artifact
rebuild and source gates must therefore run again before release readiness can
cite this tree.
