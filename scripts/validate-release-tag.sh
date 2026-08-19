#!/bin/sh
set -eu

fail() {
  echo "$1" >&2
  exit 1
}

[ "$#" -eq 1 ] || fail "release tag validation requires one VERSION argument"
tag=$1

# Restrict the input before regular-expression validation.
case "$tag" in
  ''|*[!v.0-9]*)
    fail "VERSION must be a stable vMAJOR.MINOR.PATCH tag"
    ;;
esac

# Each component is zero or a non-zero digit followed by digits.
pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
printf '%s\n' "$tag" | LC_ALL=C grep -Eq "$pattern" ||
  fail "VERSION must be a stable vMAJOR.MINOR.PATCH tag"

printf '%s\n' "$tag"
