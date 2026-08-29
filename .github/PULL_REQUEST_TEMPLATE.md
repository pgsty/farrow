## What changes

<!-- One paragraph: what this does and why it is needed. -->

## Why this way

<!-- The reasoning a reviewer cannot get from the diff: what you rejected, and
     what would break if this were done the obvious way. -->

## Verification

- [ ] `make check` passes with no output
- [ ] New or changed behaviour has a test that fails without this change
- [ ] User-visible changes are noted in `CHANGELOG.md` under `Unreleased`
- [ ] No new compatibility shim for a format that was never released
