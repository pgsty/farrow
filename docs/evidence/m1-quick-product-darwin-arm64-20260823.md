# Product quick lifecycle — macOS arm64 — 2026-08-23

Result class: native real E2E for the current product CLI on macOS arm64.
This is partial M1 evidence only; Linux, configuration/plan flags, and the
remaining M1 command surface are not complete.

## Imported-cache lifecycle

Isolated paths:

```text
project: /Users/vonng/Library/Caches/piglet/product-e2e/project-njT9Zx
data:    /Users/vonng/Library/Caches/piglet/product-e2e/data-i3Lov9
```

Commands verified:

```text
piglet image import --sha256 <u24-digest> <local-qcow2>
piglet up --json
piglet status --json
piglet up --json                 # QMP-verified no-op
piglet exec -- uname -m
piglet exec -- sh -c 'exit 17'  # Piglet returned 17
piglet stop --json
piglet status --json
piglet start --json
piglet exec -- sudo test -f /data/product-cli-canary
piglet destroy --force --json
piglet up --json                 # recreate with preserved project/key/cache
piglet stop --json
```

Observed results:

- strict `.piglet/project.json` and mirrored data-root `project.json`;
- versioned `resolved.json`, node state, and transaction journal via atomic
  fsync/rename writes;
- immutable digest cache at mode `0444`;
- QMP/process identity, readiness generation/hash, and concurrent exec locking;
- three concurrent exec calls produced success, success, and exact exit 17;
- stop/start preserved `/data`;
- destroy removed only node/runtime artifacts and preserved marker, resolved
  spec, keys, project lock, and image cache;
- recreate retained the exact private-key SHA-256
  `ee20f78d2d6174b285c7209d455e9a29ddc00ffc6b9b58a2f2d6dce937fba601`;
- final recreated VM was left stopped.

The concurrent exec probe initially exposed QEMU's one-client QMP socket race;
project-lock serialization was added and the same concurrent probe then passed.

## Strict zero-configuration lifecycle

Isolated paths:

```text
project: /Users/vonng/Library/Caches/piglet/product-e2e/zeroconfig-project-8Iiq9U
data:    /Users/vonng/Library/Caches/piglet/product-e2e/zeroconfig-data-8aLZAd
```

With no project marker, no state, and no cached image, `piglet up --json`:

1. downloaded the embedded test-only u24 HTTPS URL;
2. verified SHA-256 before acceptance;
3. rejected no redirects/downgrades and left no partial file;
4. validated a backing-free qcow2;
5. published a mode-0444 digest cache entry;
6. created the project/root/data/seed/NVRAM/state;
7. booted HVF and matched `dba` readiness.

`piglet exec -- true` passed and the VM was left stopped. This is the first
product-path proof of the no-YAML `piglet up` promise on the current Mac.

## Override and drift lifecycle

An existing stopped default project received `piglet up --cpus 4 --json`.
Piglet did not start or mutate the VM; it returned exit code 4 with a structured
`restart` drift object containing the complete before/after resolved specs.

A separate fresh project then ran:

```text
piglet up --cpus 1 --memory 2GiB --root-disk 20GiB \
  --no-data-disk --no-default-forwards --forward 19000:9000
```

Native evidence confirmed one vCPU, about 2 GiB guest memory, a grown
19,682,557,952-byte root filesystem, no `/data`, no default business listeners,
and only SSH plus 19000→9000 in resolved state/QEMU. The disposable node was
then destroyed with the scoped product command.

## Strict YAML, validation, and plan

- `piglet init quick` exported the persisted resolved spec, including actual
  materialized ports.
- `piglet validate -f <export>` reproduced the identical spec hash.
- strict YAML rejects unknown fields, multiple documents, unitless sizes,
  invalid private/DHCP addresses, reserved mounts, duplicate names/IPs/ports,
  and non-native architecture.
- read-only plan on an empty directory returned `create` without creating
  `.piglet`; persisted default returned `no-op`; `--cpus 4` returned `restart`.
- a declarative single-node user `piglet.yaml` completed a real product boot
  and scoped destroy.
- the valid two-node private fixture validates and plans as `create`, while
  `up -f` returns capability exit 3 after read-only network/lease inspection and leaves the empty
  workspace untouched. It is not silently downgraded to user mode.
- `restart`, `ssh-config`, and `logs` were exercised against the stopped
  product project; the VM was returned to stopped state.

## Marker-owned SSH integration

Using an isolated temporary home, `ssh-config --install --name e2e` created
only a mode-0700 `.ssh` directory, mode-0600 `config`, and a mode-0600
project-marker-owned fragment. A second install returned `changed=false`.
Effective `ssh -G` parsing proved `e2e-meta` resolves to dba@127.0.0.1:2222,
the exact project identity/known-hosts paths, `IdentitiesOnly yes`, and
`StrictHostKeyChecking accept-new`.

The first temp-home probe exposed that OpenSSH expands `~` from the account
database instead of process `HOME`; the Include was changed to the exact quoted
absolute fragment path. The repeated probe passed. `--remove` removed only the
matching project marker block/fragment and retained the user config file.
Symlink/unowned-fragment tests hard-fail without modifying the canary.

`project purge-keys` is dry-run-first and refuses against the real retained
node with exit 7; the complete key-tree hash stayed byte-identical. Temporary
fixtures prove forced deletion only after node absence and complete directory
preflight, while one unexpected file preserves every key. `recreate --force`
is implemented as guarded destroy followed by transactional up; it was not run
against retained evidence because it is intentionally destructive.

## Still missing for M1 completion

- Linux amd64/KVM product E2E;
- production image hosting/signing custody for a publishable build;
- global log-level/retention and project key purge/recreate command polish;
- the prerequisite M0 macOS private install/E2E gate still awaiting sudo.
