#!/usr/bin/env bash

# Strict SemVer 2.0.0 validation shared by release scripts. The v prefix is
# deliberately excluded because callers validate tags and versions separately.
farrow_is_semver() {
  local value=${1:-} base metadata prerelease core identifier
  local -a identifiers

  [[ -n ${value} && ${value} != v* ]] || return 1
  base=${value}
  if [[ ${base} == *+* ]]; then
    metadata=${base#*+}
    base=${base%%+*}
    [[ ${metadata} =~ ^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$ ]] || return 1
    IFS=. read -r -a identifiers <<<"${metadata}"
    for identifier in "${identifiers[@]}"; do
      [[ -n ${identifier} && ${identifier} =~ ^[0-9A-Za-z-]+$ ]] || return 1
    done
  fi

  core=${base}
  if [[ ${core} == *-* ]]; then
    prerelease=${core#*-}
    core=${core%%-*}
    [[ ${prerelease} =~ ^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$ ]] || return 1
    IFS=. read -r -a identifiers <<<"${prerelease}"
    for identifier in "${identifiers[@]}"; do
      [[ -n ${identifier} && ${identifier} =~ ^[0-9A-Za-z-]+$ ]] || return 1
      if [[ ${identifier} =~ ^[0-9]+$ && ${#identifier} -gt 1 && ${identifier} == 0* ]]; then
        return 1
      fi
    done
  fi

  [[ ${core} =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

farrow_is_prerelease_semver() {
  local value=${1:-}
  farrow_is_semver "${value}" && [[ ${value%%+*} == *-* ]]
}
