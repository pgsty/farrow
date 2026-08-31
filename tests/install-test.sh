#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)
temporary=$(mktemp -d "${HOME}/.farrow-install-test.XXXXXX")
dev_output=
cleanup() {
  if [[ -n ${dev_output} ]]; then
    case ${dev_output} in
      "${repo}"/bin/install-test-retention.*) rm -rf -- "${dev_output}" ;;
      *) printf 'refuse unsafe development-output cleanup: %s\n' "${dev_output}" >&2 ;;
    esac
  fi
  case ${temporary} in
    "${HOME}"/.farrow-install-test.*) rm -rf -- "${temporary}" ;;
    *) printf 'refuse unsafe installer-test cleanup: %s\n' "${temporary}" >&2 ;;
  esac
}
trap cleanup EXIT

work=${temporary}/work
release=${work}/release
source_root=${work}/source
shim=${work}/shim
tmp=${work}/tmp
install -d -m 0700 "${release}" "${source_root}" "${shim}" "${tmp}"

case $(uname -s) in
  Darwin) goos=darwin ;;
  Linux) goos=linux ;;
  *) printf 'installer test requires macOS or Linux\n' >&2; exit 3 ;;
esac
case $(uname -m) in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) printf 'installer test requires amd64 or arm64\n' >&2; exit 3 ;;
esac

make_fixture_release() {
  local fixture_version=$1 marker=$2 name
  version=${fixture_version}
  root=farrow_${version}_${goos}_${goarch}
  asset=${root}.tar.gz
  install -d -m 0700 "${source_root}/${root}/bin"
  for name in farrow farrow-hosts-helper; do
    printf '#!/usr/bin/env bash\nprintf "fixture %s %s\\n"\n' "${name}" "${marker}" >"${source_root}/${root}/bin/${name}"
    chmod 0755 "${source_root}/${root}/bin/${name}"
  done
  tar -czf "${release}/${asset}" -C "${source_root}" "${root}"
  if command -v shasum >/dev/null 2>&1; then
    digest=$(shasum -a 256 "${release}/${asset}" | awk '{print $1}')
  else
    digest=$(sha256sum "${release}/${asset}" | awk '{print $1}')
  fi
  printf '%s  %s\n' "${digest}" "${asset}" >"${release}/checksums.txt"
  printf '{"fixture":"signature bundle"}\n' >"${release}/checksums.txt.sigstore.json"
}

make_fixture_release 9.9.9 9.9.9

for tool in awk bash chmod cmp gzip install ln mkdir mktemp mv readlink rm rmdir stat tar uname; do
  target=$(command -v "${tool}")
  ln -s "${target}" "${shim}/${tool}"
done
if command -v shasum >/dev/null 2>&1; then
  ln -s "$(command -v shasum)" "${shim}/shasum"
else
  ln -s "$(command -v sha256sum)" "${shim}/sha256sum"
fi

cat >"${shim}/curl" <<'CURL_SHIM'
#!/usr/bin/env bash
set -euo pipefail
output=
expect_output=false
url=
for argument in "$@"; do
  if [[ ${expect_output} == true ]]; then
    output=${argument}
    expect_output=false
    continue
  fi
  case ${argument} in
    -o|-*o) expect_output=true ;;
    http://*|https://*) url=${argument} ;;
  esac
done
[[ -n ${url} ]] || { printf 'fake curl received no URL\n' >&2; exit 99; }
if [[ ${url} == */releases/latest ]]; then
  printf '%s' "${FAKE_LATEST_URL:-https://github.com/pgsty/farrow/releases}"
  exit 0
fi
name=${url##*/}
if [[ ${name} == checksums.txt.sigstore.json && ${FAKE_BUNDLE_MISSING:-0} == 1 ]]; then
  exit 22
fi
[[ -n ${output} && -f ${FAKE_RELEASE_ROOT}/${name} ]] || {
  printf 'unexpected fake curl request: %s -> %s\n' "${url}" "${output}" >&2
  exit 99
}
/bin/cp "${FAKE_RELEASE_ROOT}/${name}" "${output}"
CURL_SHIM
chmod 0755 "${shim}/curl"

cat >"${shim}/cosign" <<'COSIGN_SHIM'
#!/usr/bin/env bash
set -euo pipefail
[[ ${1:-} == verify-blob ]] || { printf 'unexpected fake cosign command\n' >&2; exit 99; }
exit "${FAKE_COSIGN_EXIT:-0}"
COSIGN_SHIM
chmod 0755 "${shim}/cosign"

run_installer() {
  local name=$1 expected=$2 bundle_missing=$3 cosign_exit=$4 allow_unsigned=$5 explicit_version=$6
  local install_dir=${work}/install-${name}
  local stdout=${work}/${name}.stdout stderr=${work}/${name}.stderr
  local -a environment=(
    "PATH=${shim}"
    "TMPDIR=${tmp}"
    "FARROW_INSTALL_DIR=${install_dir}"
    "FARROW_RELEASE_REPOSITORY=pgsty/farrow"
    "FAKE_RELEASE_ROOT=${release}"
    "FAKE_BUNDLE_MISSING=${bundle_missing}"
    "FAKE_COSIGN_EXIT=${cosign_exit}"
  )
  if [[ ${allow_unsigned} == 1 ]]; then
    environment+=("FARROW_INSTALL_ALLOW_UNSIGNED=1")
  fi
  if [[ ${explicit_version} == 1 ]]; then
    environment+=("FARROW_VERSION=${version}")
  else
    environment+=("FAKE_LATEST_URL=https://github.com/pgsty/farrow/releases")
  fi
  set +e
  env -u FARROW_VERSION -u FARROW_INSTALL_ALLOW_UNSIGNED -u FARROW_INSTALL_KEEP "${environment[@]}" bash "${repo}/packaging/install.sh" >"${stdout}" 2>"${stderr}"
  status=$?
  set -e
  [[ ${status} -eq ${expected} ]] || {
    printf 'installer case %s exit=%d want=%d\n' "${name}" "${status}" "${expected}" >&2
    sed -n '1,120p' "${stdout}" >&2
    sed -n '1,120p' "${stderr}" >&2
    exit 1
  }
}

run_retention_installer() {
  local name=$1 install_dir=$2 keep=$3 expected=$4
  local stdout=${work}/${name}.stdout stderr=${work}/${name}.stderr status
  set +e
  env -u FARROW_VERSION -u FARROW_INSTALL_ALLOW_UNSIGNED -u FARROW_INSTALL_KEEP \
    "PATH=${shim}" \
    "TMPDIR=${tmp}" \
    "FARROW_INSTALL_DIR=${install_dir}" \
    "FARROW_INSTALL_KEEP=${keep}" \
    "FARROW_RELEASE_REPOSITORY=pgsty/farrow" \
    "FARROW_VERSION=${version}" \
    "FAKE_RELEASE_ROOT=${release}" \
    "FAKE_BUNDLE_MISSING=0" \
    "FAKE_COSIGN_EXIT=0" \
    bash "${repo}/packaging/install.sh" >"${stdout}" 2>"${stderr}"
  status=$?
  set -e
  [[ ${status} -eq ${expected} ]] || {
    printf 'retention installer case %s exit=%d want=%d\n' "${name}" "${status}" "${expected}" >&2
    sed -n '1,120p' "${stdout}" >&2
    sed -n '1,120p' "${stderr}" >&2
    exit 1
  }
}

count_release_directories() {
  local release_root_directory=$1 entry count=0
  for entry in "${release_root_directory}"/*; do
    [[ -e ${entry} || -L ${entry} ]] || continue
    if [[ -d ${entry} && ! -L ${entry} ]]; then
      ((count += 1))
    fi
  done
  printf '%d\n' "${count}"
}

run_installer signed 0 0 0 0 1
grep -q 'Verified the release signature' "${work}/signed.stdout"
[[ -x ${work}/install-signed/farrow && -x ${work}/install-signed/farrow-hosts-helper ]]

run_installer missing-bundle 7 1 0 0 1
grep -q 'refusing checksum-only downgrade' "${work}/missing-bundle.stderr"

run_installer unsigned-override 0 1 0 1 1
grep -q 'SIGNATURE BUNDLE IS MISSING' "${work}/unsigned-override.stderr"
grep -q 'signature NOT verified' "${work}/unsigned-override.stdout"

run_installer invalid-signature 7 0 9 1 1
grep -q 'signature did not verify' "${work}/invalid-signature.stderr"

run_installer prerelease-latest 2 0 0 0 0
grep -q 'set FARROW_VERSION explicitly' "${work}/prerelease-latest.stderr"

retained_install=${work}/install-retained
for sequence in 1 2 3 4 5; do
  make_fixture_release "9.9.${sequence}" "9.9.${sequence}"
  run_retention_installer "retained-${sequence}" "${retained_install}" 3 0
  active_release=$(cd "${retained_install}/.farrow-current" && pwd -P)
  touch -t "20260101000${sequence}" "${active_release}"
done
[[ $(count_release_directories "${retained_install}/.farrow-releases") -eq 3 ]]
[[ $(readlink "${retained_install}/.farrow-current") == .farrow-releases/9.9.5-* ]]
[[ $("${retained_install}/farrow") == 'fixture farrow 9.9.5' ]]
for retired in "${retained_install}"/.farrow-releases/9.9.1-* "${retained_install}"/.farrow-releases/9.9.2-*; do
  [[ ! -e ${retired} && ! -L ${retired} ]]
done

for sequence in 6 7; do
  make_fixture_release "9.9.${sequence}" "9.9.${sequence}"
  run_retention_installer "retention-disabled-${sequence}" "${retained_install}" 0 0
done
[[ $(count_release_directories "${retained_install}/.farrow-releases") -eq 5 ]]
[[ $("${retained_install}/farrow") == 'fixture farrow 9.9.7' ]]

unsafe_install=${work}/install-unsafe-retention
make_fixture_release 9.9.8 9.9.8
run_retention_installer unsafe-seed "${unsafe_install}" 3 0
install -d -m 0700 "${work}/outside-release"
ln -s "${work}/outside-release" "${unsafe_install}/.farrow-releases/unsafe"
make_fixture_release 9.9.10 9.9.10
run_retention_installer unsafe-child "${unsafe_install}" 3 7
grep -q 'refuse unsafe retained release' "${work}/unsafe-child.stderr"
[[ -L ${unsafe_install}/.farrow-releases/unsafe ]]
[[ $("${unsafe_install}/farrow") == 'fixture farrow 9.9.10' ]]

make_fixture_release 9.9.11 9.9.11
run_retention_installer invalid-keep "${work}/install-invalid-keep" invalid 2
grep -q 'FARROW_INSTALL_KEEP must be a non-negative integer' "${work}/invalid-keep.stderr"

install -d -m 0755 "${repo}/bin"
dev_output=$(mktemp -d "${repo}/bin/install-test-retention.XXXXXX")
for sequence in 1 2 3 4; do
  FARROW_INSTALL_KEEP=2 FARROW_VERSION=dev FARROW_COMMIT=uncommitted \
    FARROW_BUILD_DATE="2026-01-01T00:00:0${sequence}Z" \
    "${repo}/packaging/build-dev.sh" "${goos}" "${goarch}" "${dev_output}" >/dev/null
  dev_active=${dev_output}/$(dirname "$(readlink "${dev_output}/farrow")")
  touch -t "20260101000${sequence}" "${dev_active}"
done
[[ $(count_release_directories "${dev_output}/.farrow-releases") -eq 2 ]]
[[ -x ${dev_output}/farrow && -x ${dev_output}/farrow-hosts-helper ]]
[[ $(dirname "$(readlink "${dev_output}/farrow")") == $(dirname "$(readlink "${dev_output}/farrow-hosts-helper")") ]]

printf 'installer signature, retained-release, and development-pair boundaries passed\n'
