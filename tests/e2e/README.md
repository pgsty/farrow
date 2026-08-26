# End-to-end scripts

These are replayable probes against real hardware, not automated pass/fail
tests. Each run writes an evidence directory outside the source tree, or under
a path you supply. A run only counts as evidence when the host, accelerator,
image digest, commands and logs are all recorded.

| Script | Requires | Does |
|---|---|---|
| `host-audit.sh` | nothing; read-only | snapshots host network and virtualization state |
| `quick-smoke.sh` | the development `farrow-m0` probe, a checksum-verified native qcow2, and a new output root | preserves the historical M0 vertical-slice lifecycle evidence; it does not exercise the public CLI |
| `quick-product-smoke.sh` | an absolute public `farrow` binary, an existing mode-0700 data root, and a new evidence root | validates zero-config Quick, boots the same contract with one explicit read-write 9p share, verifies public exec plus bounded sudo provision and guest/network/storage/share persistence through stop/start, then performs a marker-verified scoped destroy |
| `private-full-product-smoke.sh` | the same inputs plus an already installed, healthy, lease-free private network | validates the embedded `full` profile, boots the same topology with one default-read-only share on `meta`, verifies bounded parallel provision and the complete four-node product data plane and stop/start persistence, then performs a marker-verified scoped destroy |
| `private-soak.sh` | a pre-existing running four-node `full` project with its data canaries already written | runs 1–30 stop/start cycles and leak checks; it does not create or destroy the project |

The product smoke scripts create their workdir below the caller's new evidence
root with an exclusive `mkdir`, then verify both new directories are owned by
the caller and mode `0700`. Before any trap-driven stop or destroy they
revalidate the workspace marker directory, projects registry, both project
markers, UUID, owner, mode, data root, and canonical project-root containment.
If that proof fails they preserve the evidence and refuse cleanup. The scripts
use the native `stat` dialect selected explicitly for Darwin or Linux; other
host operating systems fail before project creation. If a proved-safe trap
cleanup command itself fails, its two exit codes and stdout/stderr are retained
instead of presenting cleanup as successful.

Both have the same explicit invocation shape:

```bash
tests/e2e/quick-product-smoke.sh \
  "$PWD/bin/farrow" /absolute/mode-0700/data-root /absolute/new/evidence-root
```

The private script substitutes `private-full-product-smoke.sh` in that command.
The data root must already exist; the evidence root must not exist and its
parent must already exist.

`private-full-product-smoke.sh` never installs or uninstalls the host network.
It reads `network status`, requires a healthy inactive installation, and runs
the public preflight before `up`. Host-network installation remains a separate
privileged operation that must display its exact plan and must never be
auto-approved by an E2E script.
