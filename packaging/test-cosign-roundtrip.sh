#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s <absolute-checksums-file>\n' "$0" >&2
  exit 2
fi
checksums=$1
[[ ${checksums} == /* && -f ${checksums} && ! -L ${checksums} ]] || { printf 'checksums must be an absolute regular file\n' >&2; exit 2; }
for tool in cosign jq; do
  command -v "${tool}" >/dev/null || { printf 'required signing test tool is missing: %s\n' "${tool}" >&2; exit 3; }
done
repo=$(cd "$(dirname "$0")/.." && pwd -P)
"${repo}/packaging/check-toolchain.sh" signing

temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/farrow-cosign-test.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/farrow-cosign-test.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe signing-test cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT
umask 077

# This password protects only the short-lived development key below. Production
# release keys, KMS references, identity tokens, and bundles are never accepted
# or written by this test.
export COSIGN_PASSWORD=farrow-development-test-only
key=${temporary}/ephemeral
bundle=${temporary}/checksums.sigstore.json
predicate=${temporary}/provenance-predicate.json
attestation_bundle=${temporary}/checksums.provenance.sigstore.json
cosign generate-key-pair --output-key-prefix "${key}" >/dev/null
cosign sign-blob --yes --use-signing-config=false --tlog-upload=false --key "${key}.key" \
  --bundle "${bundle}" "${checksums}" >"${temporary}/signature.txt"
# This is deliberately an offline development-key check. Production keyless
# verification in release.yml retains transparency-log enforcement.
cosign verify-blob --insecure-ignore-tlog --key "${key}.pub" --bundle "${bundle}" "${checksums}" >/dev/null
jq -e '
  .mediaType == "application/vnd.dev.sigstore.bundle.v0.3+json" and
  (.verificationMaterial.publicKey.hint | length > 0) and
  (.messageSignature.signature | length > 0)
' "${bundle}" >/dev/null

jq -n '{
  buildDefinition: {
    buildType: "https://github.com/pgsty/farrow/packaging/test-cosign-roundtrip/v1",
    externalParameters: {testOnly: true},
    internalParameters: {},
    resolvedDependencies: []
  },
  runDetails: {
    builder: {id: "https://github.com/pgsty/farrow/packaging/test-cosign-roundtrip.sh"},
    metadata: {invocationId: "ephemeral-local-test"}
  }
}' >"${predicate}"
cosign attest-blob --yes --use-signing-config=false --tlog-upload=false --key "${key}.key" \
  --predicate "${predicate}" --type slsaprovenance1 \
  --bundle "${attestation_bundle}" "${checksums}" >"${temporary}/attestation.txt"
cosign verify-blob-attestation --insecure-ignore-tlog --key "${key}.pub" --type slsaprovenance1 \
  --bundle "${attestation_bundle}" "${checksums}" >/dev/null
jq -e '
  .mediaType == "application/vnd.dev.sigstore.bundle.v0.3+json" and
  (.verificationMaterial.publicKey.hint | length > 0) and
  (.dsseEnvelope.signatures | length > 0)
' "${attestation_bundle}" >/dev/null

cp "${checksums}" "${temporary}/tampered-checksums.txt"
printf '\n# tamper canary\n' >>"${temporary}/tampered-checksums.txt"
if cosign verify-blob --insecure-ignore-tlog --key "${key}.pub" --bundle "${bundle}" \
  "${temporary}/tampered-checksums.txt" >"${temporary}/tamper.stdout" 2>"${temporary}/tamper.stderr"; then
  printf 'cosign accepted tampered checksums\n' >&2
  exit 1
fi
if cosign verify-blob-attestation --insecure-ignore-tlog --key "${key}.pub" --type slsaprovenance1 \
  --bundle "${attestation_bundle}" "${temporary}/tampered-checksums.txt" \
  >"${temporary}/tamper-attestation.stdout" 2>"${temporary}/tamper-attestation.stderr"; then
  printf 'cosign attestation accepted tampered checksums\n' >&2
  exit 1
fi
printf 'ephemeral Cosign signature/provenance bundles and tamper rejection passed\n'
