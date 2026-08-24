# Native E2E evidence

Scripts in this directory are replayable probes, not automatic claims of
success. Each run writes a dated evidence directory outside the source tree or
under a caller-provided path. A result is `native real E2E` only when the host,
accelerator, image digest, commands, and logs are recorded.

- `host-audit.sh` is read-only and safe on a development/test host.
- `quick-smoke.sh` requires an explicitly supplied, checksum-verified,
  read-only native qcow2 image and a new output root. It runs one to ten fresh
  create/boot/stop/start/stop cycles and preserves every cycle's evidence.
- macOS private and Linux private scripts will display privileged plans and
  rollback details before installation; they must not auto-approve sudo.
