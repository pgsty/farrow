# M3 Pigsty meta bootstrap — Darwin arm64 / Go 1.27 — 2026-08-24

Result class: **native real Pigsty bootstrap, deploy, sustained health,
stop/start recovery, and scoped cleanup PASS**. This is a disposable
development VM, not a production deployment.

## Scope and inputs

```text
host:             macOS arm64 / HVF
network:          socket_vmnet host, 10.10.10.0/24, bridge100, ready
profile:          meta, private, 2 vCPU, 4 GiB, 64 GiB root, 128 GiB /data
login identity:   dba UID 88, admin primary GID 88
image:            U24 arm64 standard
image SHA-256:    aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476
Pigsty source:    dab5dba333a070d96fde1f9feb41761148f2be8c
project ID:       85b32298-6fb9-4e8f-8cb9-815927adc811
spec hash:        4fcab7f80a24c0308bf30f0309629502020672e42a82cece2160e5038547c549
evidence root:    /Users/vonng/Library/Caches/piglet-bootstrap-meta-20260824.hDBumZ/evidence
```

The evidence root is mode 0700. Inventory, SSH config, source archive, and
generated user-data are mode 0600. Public documentation records hashes and
non-sensitive results only; it does not copy inventory passwords, project
private keys, CA keys, or generated service credentials.

Managed tuned inventory SHA-256:

```text
ef7749d103ddbe661073a89c5ac96469450ca86c0ffbfdfff4777e23ff2a3bfa
```

It came from schema-3 `meta -> conf/meta.yml`, used direct mode, had five typed
address tokens, applied zero address replacements and two resource-aware tune
changes (`node_tune: tiny`, `pg_conf: tiny.yml`).

## Guest and predeploy gate

The final fresh VM proved:

```text
dba:x:88:88::/home/dba:/bin/bash
admin:x:88:
/home/dba       owner 88:88 mode 0750
/home/dba/.ssh  owner 88:88 mode 0700
cloud-init      done, errors=[]
private0        10.10.10.10/24
default route   mgmt0 only
root            64 GiB
/data           128 GiB XFS
outbound HTTPS  pass
```

The isolated SSH config included `meta`, `.10`, and the reviewed Pigsty aliases
and authenticated as `dba`; no ambient `~/.ssh/config` was read.

Inside the VM:

```text
Pigsty bootstrap -k                   PASS
Ansible core 2.16.3                   PASS
bin/validate managed inventory       PASS
ansible-inventory --list             PASS
ansible all -m ping                   PASS
ansible become command id -u          0
deploy.yml --syntax-check             PASS
```

The required `deploy.yml --check --diff` was attempted first. It reached the
CA role but failed because check mode only simulated creation of
`files/pki/ca`, while the next check-mode task required the real directory.
Guest inventory/node checks had `failed=0`; localhost had this one deterministic
check-mode dependency failure. It was retained as `deploy-check.log` and was
not counted as a product deploy failure.

## Failures that changed the contract

The first real deployment used the prior name-only login contract. The image
created `dba` as UID1000; Pigsty's node role attempted to change it to UID88
while Ansible was logged in as that user, and Ubuntu rejected `usermod` with
rc=8. This run stopped at node admin and remains in `deploy.log`.

The first UID88 seed then found Ubuntu's pre-existing `admin` group at GID109.
The fail-closed identity finalizer correctly withheld the ready marker. That
run proved that `primary_group: admin` alone was insufficient.

The final seed safely verifies GID88 is free, changes or creates `admin` as
GID88 before users-groups, creates `dba` UID88/GID88, and checks passwd/group/
home ownership before readiness. Its cloud-config was accepted by the guest's
actual cloud-init schema. A second issue found during the scoped rebuild was
fixed as well: `plan` now treats a preserved project marker with no
`resolved.json` after destroy as a non-destructive `create` instead of failing.

## Final real deploy

`deploy-dba88.log` contains the complete unredacted local run. The terminal
recap was:

```text
10.10.10.10 : ok=278 changed=207 unreachable=0 failed=0 skipped=95
localhost   : ok=6   changed=4   unreachable=0 failed=0 skipped=1
```

The playbook passed its waits for etcd, HAProxy, PgBouncer, PostgreSQL,
pg_exporter, pgbouncer_exporter, and pgBackRest exporter; created `pg-meta`,
loaded the meta baseline, created the pgBackRest stanza and initial backup, and
registered PostgreSQL/Grafana/Victoria targets. No business table contents
were read.

## Full inventory matrix

Separately, all 13 custom `172.31.251.0/24` inventories were generated with
mode-0600 ownership markers and parsed by Ansible 2.16.3/PyYAML 6.0.1 on the
authorized Linux amd64 host. Exact unique host counts passed:

```text
all=7 citus=13 deb=5 deci=8 dual=2 full=4 meta=1 minio=4
oss=7 pro=7 rpm=2 simu=20 trio=3
```

The `rpm`/`deb` values prove their typed subset overlays; `deci=8` proves its
two declared idle VMs were not invented as inventory hosts.

## Sustained health and lifecycle

After external execution resumed, an independent check proved:

```text
Patroni pg-meta-1        Leader / running / PostgreSQL 18.6
PostgreSQL               accepting connections, primary
Patroni health           state=running role=primary
Grafana                  database=ok
VictoriaMetrics          OK
pgBackRest               stanza=ok; initial full backup present
PgBouncer/HAProxy/etcd    active
exporters                active
/data canary              fsynced
```

Patroni correctly listened on the private address `10.10.10.10:8008`, not
loopback; the initial loopback probe failure was corrected rather than counted
as service failure.

The first postdeploy `stop` exposed a real timeout mismatch: systemd's Vector
service can consume a 90-second stop budget, while Piglet allowed only 60
seconds. QEMU subsequently exited cleanly and status reconciled the state, but
that attempt was not a successful synchronous stop. The shared Quick/private
graceful guest timeout was raised to 120 seconds with unit/race coverage. A
fresh start restored the `/data` canary, Patroni Leader (timeline advanced),
PostgreSQL, backup visibility, Grafana and VictoriaMetrics. The corrected stop
then completed synchronously in 60 seconds, returned `state=stopped`, and
released the host-global lease.

Finally, scoped `destroy --force` removed node artifacts, QEMU/runtime paths,
and resolved state. `project purge-keys` passed plan and apply phases and
removed the exact project key set. Final checks proved an empty node directory,
no resolved state, no keys, no QEMU/runtime path, and an inactive lease. The
mode-0700 evidence root and shared digest cache remain intentionally retained.
