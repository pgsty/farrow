#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)
pipeline=${repo}/packaging/image-pipeline/build.sh
temporary_parent=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "${temporary_parent}/piglet-image-pipeline-test.XXXXXX")
temporary=$(cd "${temporary}" && pwd -P)
cleanup() {
  case ${temporary} in
    "${temporary_parent}"/piglet-image-pipeline-test.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe test cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT
umask 077
mkdir "${temporary}/bin"

cat >"${temporary}/bin/qemu-img" <<'FAKE_QEMU'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --version ]]; then
  printf 'qemu-img version 11.1.0-test\n'
  exit 0
fi
command=${1:-}
image=${!#}
scenario=$(sed -n '1p' "${image}")
safe='{"format":"qcow2","virtual-size":4294967296,"cluster-size":65536,"dirty-flag":false,"format-specific":{"type":"qcow2","data":{"compat":"1.1","compression-type":"zlib","corrupt":false,"extended-l2":false,"incompatible-features":[]}}}'
case ${command} in
  info)
    chain=false
    for argument in "$@"; do
      [[ ${argument} == --backing-chain ]] && chain=true
    done
    if [[ ${chain} == true ]]; then
      if [[ ${scenario} == FAKEQCOW:chain ]]; then
        printf '[%s,%s]\n' "${safe}" "${safe}"
      else
        printf '[%s]\n' "${safe}"
      fi
      exit 0
    fi
    case ${scenario} in
      FAKEQCOW:backing) printf '{"format":"qcow2","virtual-size":4294967296,"backing-filename":"/tmp/base.qcow2"}\n' ;;
      FAKEQCOW:data) printf '{"format":"qcow2","virtual-size":4294967296,"format-specific":{"type":"qcow2","data":{"data-file":"/tmp/data.raw"}}}\n' ;;
      FAKEQCOW:encrypted) printf '{"format":"qcow2","virtual-size":4294967296,"encrypted":true}\n' ;;
      FAKEQCOW:incompatible) printf '{"format":"qcow2","virtual-size":4294967296,"format-specific":{"type":"qcow2","data":{"incompatible-features":["mystery-bit"]}}}\n' ;;
      FAKEQCOW:extended) printf '{"format":"qcow2","virtual-size":4294967296,"format-specific":{"type":"qcow2","data":{"extended-l2":true}}}\n' ;;
      FAKEQCOW:corrupt) printf '{"format":"qcow2","virtual-size":4294967296,"format-specific":{"type":"qcow2","data":{"corrupt":true}}}\n' ;;
      FAKEQCOW:dirty) printf '{"format":"qcow2","virtual-size":4294967296,"dirty-flag":true}\n' ;;
      FAKEQCOW:raw) printf '{"format":"raw","virtual-size":4294967296}\n' ;;
      *) printf '%s\n' "${safe}" ;;
    esac
    ;;
  check)
    if [[ ${scenario} == FAKEQCOW:check-error ]]; then
      printf '{"corruptions":0,"check-errors":1}\n'
    else
      printf '{"corruptions":0,"check-errors":0}\n'
    fi
    ;;
  *) printf 'unexpected fake qemu-img command: %s\n' "${command}" >&2; exit 64 ;;
esac
FAKE_QEMU
chmod 0755 "${temporary}/bin/qemu-img"

cat >"${temporary}/bin/virt-customize" <<'FAKE_CUSTOMIZE'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --version ]]; then
  printf 'virt-customize 1.54.0-test\n'
  exit 0
fi
for secret in AWS_SECRET_ACCESS_KEY GITHUB_TOKEN COSIGN_PASSWORD; do
  [[ -z ${!secret:-} ]] || { printf 'host secret leaked to customizer: %s\n' "${secret}" >&2; exit 42; }
done
image=
no_network=false
previous=
for argument in "$@"; do
  if [[ ${previous} == -a ]]; then image=${argument}; fi
  [[ ${argument} == --no-network ]] && no_network=true
  previous=${argument}
done
[[ ${no_network} == true && -n ${image} && -f ${image} ]] || exit 43
printf '\nOFFLINE-NORMALIZED\n' >>"${image}"
FAKE_CUSTOMIZE
chmod 0755 "${temporary}/bin/virt-customize"

cat >"${temporary}/bin/virt-cat" <<'FAKE_CAT'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --version ]]; then
  printf 'virt-cat 1.54.0-test\n'
  exit 0
fi
guest_path=${!#}
case ${guest_path} in
  /var/lib/piglet-image/normalization.json)
    printf '{"schema":1,"recipe":"piglet-official-image-normalization-v1","source_user":"ubuntu","source_date_epoch":1787486400,"dba_uid":88,"admin_gid":88,"credential_hygiene":"applied"}\n'
    ;;
  /etc/passwd) printf 'root:x:0:0:root:/root:/bin/bash\ndba:x:88:88::/home/dba:/bin/bash\n' ;;
  /etc/group) printf 'root:x:0:\nadmin:x:88:dba\n' ;;
  *) printf 'unexpected guest path: %s\n' "${guest_path}" >&2; exit 44 ;;
esac
FAKE_CAT
chmod 0755 "${temporary}/bin/virt-cat"

digest() {
  python3 - "$1" <<'PY'
import hashlib
import pathlib
import sys
print(hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest())
PY
}

make_source() {
  local path=$1 scenario=$2
  printf 'FAKEQCOW:%s\nfixture-payload\n' "${scenario}" >"${path}"
}

run_candidate() {
  local source=$1 output=$2 mode=${3:-validate}
  local source_sha
  source_sha=$(digest "${source}")
  "${pipeline}" \
    --mode "${mode}" \
    --source "${source}" \
    --expected-sha256 "${source_sha}" \
    --output "${output}" \
    --name u24 --release 20260801.0.0 --arch amd64 \
    --source-user ubuntu --boot uefi --license NOASSERTION \
    --source-uri 'https://cloud-images.example.invalid/noble/20260801/noble-amd64.qcow2' \
    --artifact-url 'https://images.example.invalid/u24/20260801.0.0/{sha256}.qcow2' \
    --source-date-epoch 1787486400 --manifest-version 2026082403 \
    --qemu-img "${temporary}/bin/qemu-img" \
    --virt-customize "${temporary}/bin/virt-customize" \
    --virt-cat "${temporary}/bin/virt-cat"
}

verify_bundle() {
  python3 - "$1" <<'PY'
import hashlib
import json
import pathlib
import stat
import sys

root = pathlib.Path(sys.argv[1])
files = sorted(p.name for p in root.iterdir())
assert len(files) == 8, files
assert {
    "build-recipe.json", "checksums.txt", "manifest-candidate.json", "normalize-guest.sh",
    "provenance.intoto.json", "sbom.spdx.json", "validation.json",
}.issubset(files)
artifacts = [p for p in root.iterdir() if p.suffix == ".qcow2"]
assert len(artifacts) == 1
assert stat.S_IMODE(artifacts[0].stat().st_mode) == 0o444
assert int(artifacts[0].stat().st_mtime) == 1787486400
records = {}
for line in (root / "checksums.txt").read_text().splitlines():
    checksum, name = line.split("  ", 1)
    assert "/" not in name and name not in records
    records[name] = checksum
assert len(records) == 7
for name, expected in records.items():
    assert hashlib.sha256((root / name).read_bytes()).hexdigest() == expected
manifest = json.loads((root / "manifest-candidate.json").read_text())
entry = manifest["images"]["u24"]["releases"]["20260801.0.0"]["amd64"]
assert entry["status"] == "testing" and entry["format"] == "qcow2"
assert entry["sha256"] == hashlib.sha256(artifacts[0].read_bytes()).hexdigest()
assert entry["sha256"] in entry["url"]
validation = json.loads((root / "validation.json").read_text())
assert validation["promotion"]["eligible"] is False
assert validation["signing"]["performed"] is False
assert validation["inspection_after"]["one_element_backing_chain"] is True
sbom = json.loads((root / "sbom.spdx.json").read_text())
assert sbom["spdxVersion"] == "SPDX-2.3"
provenance = json.loads((root / "provenance.intoto.json").read_text())
assert provenance["predicateType"] == "https://slsa.dev/provenance/v1"
PY
}

good=${temporary}/good.qcow2
make_source "${good}" success
run_candidate "${good}" "${temporary}/validate-one" validate >"${temporary}/validate.stdout"
grep -q 'NATIVE MUTATION NOT RUN' "${temporary}/validate.stdout"
verify_bundle "${temporary}/validate-one"
python3 - "${temporary}/validate-one" "${good}" <<'PY'
import json
import pathlib
import sys
root, source = map(pathlib.Path, sys.argv[1:])
artifact = next(root.glob("*.qcow2"))
assert artifact.read_bytes() == source.read_bytes()
validation = json.loads((root / "validation.json").read_text())
assert validation["native_mutation"]["status"] == "not-run-validation-only"
manifest = json.loads((root / "manifest-candidate.json").read_text())
entry = manifest["images"]["u24"]["releases"]["20260801.0.0"]["amd64"]
assert entry["provenance"].startswith("UNPUBLISHABLE VALIDATION-ONLY")
assert entry["source_user"] == "ubuntu"
PY

run_candidate "${good}" "${temporary}/validate-two" validate >/dev/null
cmp "${temporary}/validate-one/checksums.txt" "${temporary}/validate-two/checksums.txt"
for first in "${temporary}/validate-one"/*; do
  name=$(basename "${first}")
  cmp "${first}" "${temporary}/validate-two/${name}"
done

run_candidate "${good}" "${temporary}/offline" offline >"${temporary}/offline.stdout"
grep -q 'offline normalization completed' "${temporary}/offline.stdout"
verify_bundle "${temporary}/offline"
python3 - "${temporary}/offline" "$(digest "${good}")" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
source_digest = sys.argv[2]
validation = json.loads((root / "validation.json").read_text())
assert validation["native_mutation"]["status"] == "completed"
assert validation["native_mutation"]["marker"]["dba_uid"] == 88
assert validation["artifact"]["sha256"] != source_digest
manifest = json.loads((root / "manifest-candidate.json").read_text())
entry = manifest["images"]["u24"]["releases"]["20260801.0.0"]["amd64"]
assert entry["source_user"] == "dba"
assert not entry["provenance"].startswith("UNPUBLISHABLE")
PY

expect_policy_rejection() {
  local scenario=$1
  local source=${temporary}/${scenario}.qcow2 output=${temporary}/reject-${scenario}
  make_source "${source}" "${scenario}"
  if run_candidate "${source}" "${output}" validate >"${temporary}/${scenario}.stdout" 2>"${temporary}/${scenario}.stderr"; then
    printf 'unsafe scenario was accepted: %s\n' "${scenario}" >&2
    exit 1
  fi
  [[ ! -e ${output} ]]
}
for scenario in backing data encrypted incompatible extended corrupt dirty raw chain check-error; do
  expect_policy_rejection "${scenario}"
done

bad_output=${temporary}/bad-sha-output
if "${pipeline}" --mode validate --source "${good}" --expected-sha256 "$(printf '0%.0s' {1..64})" \
  --output "${bad_output}" --name u24 --release 1 --arch amd64 --source-user ubuntu --boot uefi \
  --license NOASSERTION --artifact-url 'https://images.example.invalid/u24/1/image.qcow2' \
  --source-date-epoch 1787486400 --manifest-version 1 --qemu-img "${temporary}/bin/qemu-img" \
  >"${temporary}/bad-sha.stdout" 2>"${temporary}/bad-sha.stderr"; then
  printf 'bad source SHA was accepted\n' >&2
  exit 1
fi
[[ ! -e ${bad_output} ]]
grep -q 'source SHA-256 mismatch' "${temporary}/bad-sha.stderr"

ln -s "${good}" "${temporary}/source-link.qcow2"
if run_candidate "${temporary}/source-link.qcow2" "${temporary}/symlink-output" validate >/dev/null 2>&1; then
  printf 'symlink source was accepted\n' >&2
  exit 1
fi
[[ ! -e ${temporary}/symlink-output ]]

existing=${temporary}/existing-output
mkdir "${existing}"
if run_candidate "${good}" "${existing}" validate >/dev/null 2>&1; then
  printf 'existing output was replaced\n' >&2
  exit 1
fi
[[ -d ${existing} && -z $(find "${existing}" -mindepth 1 -print -quit) ]]

mkdir "${temporary}/path-component"
noncanonical=${temporary}/path-component/../noncanonical-output
if run_candidate "${good}" "${noncanonical}" validate >/dev/null 2>"${temporary}/noncanonical.stderr"; then
  printf 'non-canonical output path was accepted\n' >&2
  exit 1
fi
[[ ! -e ${temporary}/noncanonical-output ]]
grep -q 'output path must already be canonical' "${temporary}/noncanonical.stderr"

moving=${temporary}/moving-output
good_sha=$(digest "${good}")
if "${pipeline}" --mode validate --source "${good}" --expected-sha256 "${good_sha}" \
  --output "${moving}" --name u24 --release 1 --arch amd64 --source-user ubuntu --boot uefi \
  --license NOASSERTION --artifact-url 'https://images.example.invalid/u24/latest/image.qcow2' \
  --source-date-epoch 1787486400 --manifest-version 1 --qemu-img "${temporary}/bin/qemu-img" \
  >/dev/null 2>"${temporary}/moving.stderr"; then
  printf 'moving artifact URL was accepted\n' >&2
  exit 1
fi
[[ ! -e ${moving} ]]

grep -q -- '--no-network' "${repo}/packaging/image-pipeline/README.md"
grep -q 'UID or GID 88' "${repo}/packaging/image-pipeline/README.md"
grep -q "usermod --password '!'" "${repo}/packaging/image-pipeline/normalize-guest.sh"
grep -q 'rm -f -- /etc/ssh/ssh_host_' "${repo}/packaging/image-pipeline/normalize-guest.sh"
grep -q 'restorecon -RF /home/dba' "${repo}/packaging/image-pipeline/normalize-guest.sh"
if grep -Eq '\b(curl|wget|ssh-keygen)\b' "${repo}/packaging/image-pipeline/normalize-guest.sh"; then
  printf 'offline normalization script unexpectedly uses network/key-generation tools\n' >&2
  exit 1
fi

printf 'image pipeline validate/reproducibility/rejection and simulated offline boundaries passed\n'
printf 'SKIP native offline guest mutation: fake tools tested the boundary; run tests/image-pipeline-native-test.sh with an explicit source\n'
