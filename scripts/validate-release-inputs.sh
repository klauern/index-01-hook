#!/bin/sh
set -eu

fail() {
  echo "$1" >&2
  exit 1
}

is_match() {
  value=$1
  pattern=$2
  case "$value" in
    *'
'*) return 1 ;;
  esac
  printf '%s\n' "$value" | LC_ALL=C grep -Eq "$pattern"
}

validate_immutable_image() {
  value=$1
  label=$2
  pattern='^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?/[a-z0-9]+([._/-][a-z0-9]+)*@sha256:[0-9a-f]{64}$'
  is_match "$value" "$pattern" ||
    fail "$label must be a complete registry image reference with a sha256 digest"
}

command=${1:-}
case "$command" in
  metadata)
    [ "$#" -eq 4 ] || fail "metadata validation requires VERSION, COMMIT, and BUILD_DATE"
    version=$2
    commit=$3
    build_date=$4
    is_match "$version" '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' ||
      fail "VERSION must be a v-prefixed semantic version"
    is_match "$commit" '^[0-9a-f]{7,64}$' ||
      fail "COMMIT must contain 7 to 64 lowercase hexadecimal characters"
    is_match "$build_date" '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' ||
      fail "BUILD_DATE must use the UTC YYYY-MM-DDTHH:MM:SSZ format"
    ;;
  immutable-image)
    [ "$#" -eq 3 ] || fail "immutable image validation requires a value and label"
    validate_immutable_image "$2" "$3"
    ;;
  output-image)
    [ "$#" -eq 2 ] || fail "output image validation requires IMAGE_TAG"
    pattern='^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?/[a-z0-9]+([._/-][a-z0-9]+)*:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$'
    is_match "$2" "$pattern" ||
      fail "IMAGE_TAG must be a complete registry image name with one valid tag"
    ;;
  *)
    fail "validation command must be metadata, immutable-image, or output-image"
    ;;
esac
