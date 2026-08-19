#!/bin/sh
# shellcheck disable=SC2016
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
validator=$script_dir/validate-release-tag.sh
test_root=$(CDPATH='' cd "$(mktemp -d "${TMPDIR:-/tmp}/validate-release-tag-test.XXXXXX")" && pwd -P)
trap 'rm -rf -- "$test_root"' EXIT HUP INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_valid() {
  expected=$1
  shift
  actual=$($validator "$@") || fail "rejected valid tag: $expected"
  [ "$actual" = "$expected" ] || fail "validator changed valid tag: $expected"
}

expect_invalid() {
  name=$1
  shift
  if output=$($validator "$@" 2>"$test_root/$name.stderr"); then
    fail "accepted invalid tag: $name"
  fi
  [ -z "$output" ] || fail "emitted a tag for invalid input: $name"
}

expect_valid v0.0.0 v0.0.0
expect_valid v1.0.0 v1.0.0
expect_valid v0.1.0 v0.1.0
expect_valid v0.0.1 v0.0.1
expect_valid v10.20.30 v10.20.30
expect_valid v999999999999999999999999.1.0 v999999999999999999999999.1.0

expect_invalid leading-zero-major v01.2.3
expect_invalid leading-zero-minor v1.02.3
expect_invalid leading-zero-patch v1.2.03
expect_invalid prerelease v1.2.3-rc.1
expect_invalid build-metadata v1.2.3+build.1
expect_invalid whitespace-space 'v1.2.3 '
expect_invalid whitespace-tab "v1.2.3\t"
expect_invalid newline "v1.2.3
"
expect_invalid shell-semicolon 'v1.2.3;id'
expect_invalid shell-substitution 'v1.2.3$(id)'
expect_invalid shell-backtick 'v1.2.3`id`'
expect_invalid missing-v 1.2.3
expect_invalid extra-component v1.2.3.4
expect_invalid missing-major v.2.3
expect_invalid missing-minor v1..3
expect_invalid missing-patch v1.2.
expect_invalid empty ''
if "$validator" >"$test_root/missing.stdout" 2>"$test_root/missing.stderr"; then
  fail "accepted a missing VERSION"
fi
[ ! -s "$test_root/missing.stdout" ] || fail "emitted a tag for a missing VERSION"

printf '%s\n' 'PASS: release tag validation'
