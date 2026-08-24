# M4 source, migration, and security review — 2026-08-24

> Historical Go 1.26.7 record. Current Go 1.27 and owner-scope verification is
> recorded in `m4-owner-scope-go127-20260824.md`.

Result class: native source/tooling verification on macOS arm64 plus read-only
product-state checks. This does not close the failed guest entry or external GA
gates.

## Final source gates

Run from `/Users/vonng/pgsty/piglet` with Go 1.26.7:

```text
go test ./...                 PASS
go test -race ./...           PASS
go vet ./...                  PASS
staticcheck ./...             PASS
darwin/arm64 cross build      PASS
darwin/amd64 cross build      PASS
linux/amd64 cross build       PASS
linux/arm64 cross build       PASS
govulncheck ./...             PASS: 0 reachable vulnerabilities
all Bash syntax              PASS
ShellCheck 0.11.0            PASS with no output
Actionlint 1.7.12            PASS with no output
goreleaser check              PASS
Pigsty wrapper tests          PASS
13 profiles / 85 nodes       PASS against pinned Pigsty source/provider parity
```

`govulncheck` also reported one advisory in a required module whose vulnerable
symbols are not called; it found 0 vulnerabilities in imported packages and 0
reachable vulnerabilities.

## Dependency and license review

`go list -deps -json` for the shipped CLI/helper returned exactly six external
modules: minisign, go-diskfs, times, yaml/v3, x/crypto, and x/sys at the versions
in `THIRD_PARTY_LICENSES.md`. `packaging/verify-licenses.sh` compared each exact
upstream license file with the checked-in shipping notice. SHA-256 values:

```text
d2e73e3423b8898d7d2fba4e55a2abb43969603fe32fdd468f197ff238969d28  aead.dev/minisign
22fcc7885cdba5aeac7c0e983fe2d7f3323629aae1ad0b99bab5ebc52a2f3485  github.com/diskfs/go-diskfs
079087ce194827595700a925b01d911f465565d96c5ed2716bcc50ce76855fd5  github.com/djherbis/times
d18f6323b71b0b768bb5e9616e36da390fbd39369a81807cca352de4e4e6aa0b  go.yaml.in/yaml/v3
911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad  golang.org/x/crypto
911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad  golang.org/x/sys
```

No Lima or Vagrant module/source is linked or copied.

## N-1 state migration

Schema-0 fixtures exercised full project/node/transaction migration:

- non-mutating dry-run and deterministic action report;
- mode-0600 backup-first apply and atomic schema-1 publication;
- new `piglet_version` injection and strict full-file validation;
- idempotent second apply with JSON `actions: []`;
- newer-schema refusal without backup or overwrite;
- running/transitional or non-empty process identity refusal;
- malformed later child refusal before any backup/write;
- owner/mode/type/link-count/size and source-byte recheck;
- project lock and CLI active-private-lease exit-6 guard.

The latest native CLI read the retained stopped four-node Darwin project
`6f9f8b19-7034-447c-8b9c-8588677ca77e` using
`project upgrade-state --dry-run --json` and returned schema 1, `apply:false`,
and `actions:[]` without mutation. No prior production release/schema exists,
so this is an N-1 fixture/integration result, not a field-upgrade claim.

## Host conflict and helper pairing

The final local helper SHA is
`d3977345983565e29d238cc66b6596bca8c8464dff7f89e9636e818e2c747336`.
It equals the existing root:wheel mode-0755
`/opt/piglet/libexec/piglet-hosts-helper`, and the rebuilt CLI contains that
exact digest. No privileged file needed replacement.
The final root read-only `network status --json` reported both private-network
and private-route `ok`, with the global lease available and inactive.

With no Piglet private lease, the unrelated VM at `10.10.10.10:22` remained
running. `doctor --json` returned exit 3 with a typed
`private-address-conflict` error. A fresh exact `profiles/full.yaml` plan also
returned exit 3, mentioned `.10`, and created neither `.piglet` state nor data
files. The unrelated VM was not stopped or modified.

## Remaining security/release facts

- Production image-manifest and release signing custody are not assigned.
- The GitHub OIDC workflow is syntax-checked but unrun.
- The earlier Darwin shared failure was later shown to be subnet-confounded;
  clean alternate-subnet shared E2E passes and current source accepts it.
- EL8 arm64 is a retained historical native boot failure; the later owner
  decision removes EL8 from the v1 target and TCG is not used.
- Host reboot, Tier-2 native hosts, Pigsty bootstrap/Vagrant comparison, and an
  RPM-family private host remain not run.
