# Contributing to Farrow

## Before you start

Farrow is pre-1.0 and deliberately small. The design has a few ratified
non-negotiables, and a change that contradicts one of them will be declined no
matter how well it is implemented:

- **Exactly one deployment per user.** No projects, no leases, no workspaces.
- **One inventory format.** A Pigsty-compatible Ansible inventory is the only
  configuration. `farrow.yml` is preferred over `pigsty.yml` only as a filename.
- **The `vm_*` namespace is strict; everything else is opaque.** An unknown
  `vm_*` key is a hard error. A non-`vm_*` key is never validated.
- **Absence never destroys.** Removing a host from the inventory does not delete
  a machine. Destruction is always explicit and confirmed.
- **One helper binary.** Privileged work goes through `farrow-hosts-helper` or
  the reviewed network plan, and nowhere else.

Open an issue before a large change so we can agree on the shape first.

## Development loop

```bash
make build     # build ./bin/farrow and ./bin/farrow-hosts-helper
make test      # unit tests
make race      # race detector
make check     # everything CI runs, before you push
```

`make check` runs module verification, shell syntax checks, unit and race tests,
`go vet`, Staticcheck, `govulncheck`, cross-compilation for all four supported
targets, the image-pipeline boundary tests, and the dependency-license
inventory. It must pass with no output before a pull request is ready.

The toolchain is pinned in `packaging/toolchain.env` and CI asserts the exact
versions, so install those versions locally:

```bash
go install honnef.co/go/tools/cmd/staticcheck@v0.8.1
go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
```

Changes under `packaging/`, `.goreleaser.yaml`, or the `Makefile` also run the
`packaging` workflow, which builds and verifies a complete snapshot release.
Run it locally with `make release-snapshot` if you have GoReleaser, nFPM, and
Syft at the pinned versions.

## House style

The code aims to read as one voice. Match what is already there.

- **Comments explain why, never what.** If a comment restates the code, delete
  it. If a line looks arbitrary, the comment says what would break otherwise.
- **Names are words, not abbreviations.** `resolved`, `candidate`, `imageRecord`
  — not `res`, `c`, `img`.
- **Errors are lowercase, specific, and actionable.** Say what was wrong and
  what to do: `hosts target changed after review; refusing stale plan`.
- **No map iteration reaching output.** Sort keys. Identical input must produce
  identical output and identical errors.
- **Fail closed.** When identity, ownership, or a digest cannot be proven,
  refuse rather than proceed.
- **No compatibility shims for formats that were never released.** Farrow reads
  exactly one catalog schema and one inventory format.

## Tests

- A bug fix comes with a test that fails before it and passes after.
- Tests must not depend on the machine they run on: no hardcoded UIDs, home
  directories, network access, or the presence of a specific QEMU build. Skip
  cleanly when an external tool is genuinely required.
- Prefer testing the contract over the implementation. Assert the behaviour a
  user or a script would observe.

## Commits and pull requests

- One logical change per commit, with a subject in the imperative mood:
  `fix: reconcile SSH config across lifecycle`.
- Explain the reasoning in the body when it is not obvious from the diff.
- Note user-visible changes in `CHANGELOG.md` under `Unreleased`.

## Security issues

Do not open a public issue. Follow [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under the Apache-2.0 license that covers this
repository.
