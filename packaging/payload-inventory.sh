#!/usr/bin/env bash

# Shared release payload inventories. Keep the fixed application files here;
# dependency licenses are derived from the license corpus that is validated by
# verify-licenses.sh and copied wholesale by every packaging path.

farrow_common_payload_paths() {
  local repo=$1
  local licenses
  [[ -d ${repo}/third_party/licenses ]] || {
    printf 'missing dependency license directory: %s\n' "${repo}/third_party/licenses" >&2
    return 1
  }
  licenses=$(find "${repo}/third_party/licenses" -mindepth 1 -maxdepth 1 -type f -print | LC_ALL=C sort) || return
  printf '%s\n' \
    LICENSE \
    README.md \
    THIRD_PARTY_LICENSES.md \
    docs/architecture.md \
    docs/cli.md \
    docs/config.md \
    docs/development.md \
    docs/getting-started.md \
    docs/images.md \
    docs/networking.md \
    docs/phase-2.md \
    docs/pigsty.md \
    docs/security.md \
    docs/status.md \
    docs/troubleshooting.md \
    schemas/farrow-v1.schema.json \
    tests/e2e/README.md
  if [[ -n ${licenses} ]]; then
    while IFS= read -r license; do
      printf 'third_party/licenses/%s\n' "${license##*/}"
    done <<<"${licenses}"
  fi
}

farrow_archive_payload_paths() {
  farrow_common_payload_paths "$1" || return
  printf '%s\n' bin/farrow bin/farrow-hosts-helper bin/pigsty-vm
}

farrow_development_archive_payload_paths() {
  printf '%s\n' BUILD_INFO.json
  farrow_archive_payload_paths "$1"
}

farrow_linux_package_payload_paths() {
  local repo=$1
  local common
  printf '%s\n' \
    opt/farrow/libexec/farrow-hosts-helper \
    usr/bin/farrow \
    usr/bin/pigsty-vm \
    usr/share/doc/farrow/BUILD_INFO.json
  common=$(farrow_common_payload_paths "${repo}") || return
  while IFS= read -r path; do
    case ${path} in
      LICENSE|README.md|THIRD_PARTY_LICENSES.md|docs/*|tests/*)
        printf 'usr/share/doc/farrow/%s\n' "${path}"
        ;;
      schemas/*)
        printf 'usr/share/farrow/%s\n' "${path}"
        ;;
      third_party/licenses/*)
        printf 'usr/share/doc/farrow/licenses/%s\n' "${path##*/}"
        ;;
      *)
        printf 'unsupported common payload path: %s\n' "${path}" >&2
        return 1
        ;;
    esac
  done <<<"${common}"
}
