#!/usr/bin/env bash

# Shared release payload inventories. Dependency license names come from the
# single staging script used by every packaging path.

farrow_common_payload_paths() {
  local repo=$1
  local licenses
  licenses=$("${repo}/packaging/dependency-licenses.sh" list) || return
  printf '%s\n' \
    LICENSE \
    README.md
  if [[ -n ${licenses} ]]; then
    while IFS= read -r license; do
      printf 'licenses/%s\n' "${license}"
    done <<<"${licenses}"
  fi
}

farrow_archive_payload_paths() {
  farrow_common_payload_paths "$1" || return
  printf '%s\n' bin/farrow bin/farrow-hosts-helper
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
    usr/share/doc/farrow/BUILD_INFO.json
  common=$(farrow_common_payload_paths "${repo}") || return
  while IFS= read -r path; do
    case ${path} in
      LICENSE|README.md)
        printf 'usr/share/doc/farrow/%s\n' "${path}"
        ;;
      licenses/*)
        printf 'usr/share/doc/farrow/licenses/%s\n' "${path##*/}"
        ;;
      *)
        printf 'unsupported common payload path: %s\n' "${path}" >&2
        return 1
        ;;
    esac
  done <<<"${common}"
}
