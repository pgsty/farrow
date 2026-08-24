# Signed manifest sync evidence — 2026-08-23

Result class: unit/integration and local CLI E2E passed with dedicated
development-only minisign roots. Production key custody remains an owner gate.

## Implemented contract

- embedded schema-1 JSON catalog and monotonic numeric version;
- exact-byte minisign verification, including trusted comment;
- two embedded public roots and unknown-key rejection;
- local/offline sidecar and HTTPS `<manifest>.minisig` sync;
- HTTPS-only redirects, five-hop limit, and bounded manifest/signature sizes;
- strict JSON unknown/trailing-field rejection and artifact semantic checks;
- same-version/different-bytes equivocation rejection;
- high-water rollback rejection and explicit `--allow-downgrade` activation;
- immutable versioned manifest/signature files plus atomic active-state pointer;
- `reset-manifest` restores embedded while preserving the high-water mark;
- active catalog is re-hashed and reverified whenever read by `image list` or
  `up`; existing node state/backing remains digest-pinned.

## CLI E2E

An isolated `PIGLET_DATA_HOME` began on embedded version `2026082301`.
A version `2026082311` catalog signed by development root
`E87A2D0D9F49B03B` was activated from a local path. `image list --json`
reverified and reported the selected version/digest/key/source. Reset then
reported:

```text
active: embedded
active_version: 2026082301
highest_version: 2026082311
```

The same suite signs with the standby development root, rejects tampered bytes,
rejects a freshly generated unknown root, rejects rollback without the flag,
accepts explicit downgrade while keeping the high-water mark, rejects
same-version equivocation, verifies HTTPS sidecars, and rejects HTTPS-to-HTTP
redirects.

## Security boundary

The two corresponding private keys appear only in `_test.go` as dedicated test
fixtures. They are not production credentials. Piglet v1.0 release remains
blocked until the owner provides production active/standby public roots,
offline private-key custodians, and the signing procedure.

## Image info, pull, and prune

From the stopped native product project, `image info --json u24` selected the
persisted project data root, active embedded manifest entry, and validated
cached metadata. It re-hashed the 228,786,176-byte immutable qcow2, checked its
backing-free format/virtual size, strict mode-0600 metadata, digest, and source.

`image pull --json u24` returned the same validated digest path without a
network request. The aggregate cache-content hash was byte-identical before and
after info+pull.

`image prune --dry-run --json` scanned marker-verified projects and pending
transactions under the same data root, conservatively retained every active
manifest digest plus every node-state digest, proposed no deletion, and left
the cache byte-identical. Apply requires `--yes`; unit tests prove dry-run
preservation, exact unreferenced image/metadata pair deletion, referenced-image
retention, immutable qcow2 validation, and metadata symlink/type/mode refusal.
