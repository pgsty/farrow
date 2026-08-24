# Private lifecycle node selectors — Darwin arm64 — 2026-08-24

Result class: **native two-node lifecycle/lease E2E** for selected
`plan/up/start/stop/restart/status` using the current all-`dba` `dual` profile.

- Window: `2026-08-24T07:32:03Z`–`07:33:13Z`.
- Retained mode-0700 root:
  `/Users/vonng/Library/Caches/piglet/selectors-dual-darwin-go127-20260824-01`.
- Piglet SHA-256:
  `1d348c52e3fa0fd6981a3658a43070c3da408f6182cee8ec17c491e0067c1f32`.
- `dual` profile SHA-256:
  `a312ca0472c75a897fbe1eb8d98ffb4fac8aacfc5797c5044d6a1862b134eae8`.
- Project `757ab860-0d0e-4fe8-8639-42db71ded51e`.
- Evidence checksum-list SHA-256:
  `225e3bf5fdaab1785a0ef1fc514c6aad2b855fb84cfc3fb52c35303eb329709a`.

The exact sequence proved:

1. `plan -f dual.yaml meta-2` returned only `meta-2`, action create.
2. `up -f dual.yaml meta-2` prepared the complete two-node topology but
   started only `meta-2`.
3. Unfiltered status reported `meta-1=prepared`, `meta-2=running`.
4. `start meta-1` started only the prepared control node.
5. `stop meta-2` stopped only that node; `meta-1` remained running and the
   host-global lease remained active.
6. Mixed status reported `meta-1=running`, `meta-2=stopped`.
7. `restart meta-1` stopped/restarted only the selected node while preserving
   the stopped peer reservation.
8. Final `stop meta-1` left both nodes stopped and released the lease.
9. All four root/data qcows passed `qemu-img check`; full project destroy
   returned both nodes absent and left no project QEMU.

The first discarded run exposed a lease-phase bug: starting a selected node had
rebuilt the full reservation with unselected peers reset to `reserved`, which
blocked final ordinary release. `startExisting` now synchronizes the complete
persisted node-state set immediately after lease acquire, before selected start.
The retained clean run proves the corrected sequence.

Unknown and duplicate selectors fail before mutation. Quick accepts only its
single `meta` selector. Partial `destroy`/`recreate` remain intentionally
unsupported because deleting only part of a declarative topology needs a
separate state-model decision; those commands require the complete project.
